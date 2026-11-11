package integrations

// Shopify webhooks. Shopify signs each delivery: X-Shopify-Hmac-Sha256 is the base64 HMAC-SHA256
// of the raw body with the webhook signing secret. Three topics become Pulse events:
//
//	orders/create     → order_completed {order_id, order_number, amount, revenue, currency, items, …}
//	customers/create  → customer_created (the contact's name, phone and Shopify id)
//	checkouts/create, checkouts/update → cart_updated {checkout_token, amount, currency, items, url, …}
//
// Each carries the customer's attributes, so the contact is identified (by email, else phone).
// Completed checkouts are skipped: the order arrives as orders/create.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"pulse/internal/attr"
)

// Shopify event names.
const (
	EventOrderCompleted  = "order_completed"
	EventCustomerCreated = "customer_created"
	EventCartUpdated     = "cart_updated"
)

// ShopifyTopics are the webhook topics Pulse understands.
var ShopifyTopics = []string{"orders/create", "customers/create", "checkouts/create", "checkouts/update"}

// VerifyShopify checks the X-Shopify-Hmac-Sha256 header of a delivery.
func VerifyShopify(secret string, body []byte, header string) bool {
	if secret == "" || header == "" {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	want := base64.StdEncoding.EncodeToString(m.Sum(nil))
	return hmac.Equal([]byte(want), []byte(strings.TrimSpace(header)))
}

// SignShopify signs body like Shopify does (tests and the sample in the UI).
func SignShopify(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return base64.StdEncoding.EncodeToString(m.Sum(nil))
}

// Inbound is what one delivery means: who it's about and the event to record. The API resolves
// the contact (an existing one with that email or phone, else a new one).
type Inbound struct {
	Email      string
	Phone      string
	CustomerID string
	Name       string // the event name
	Props      map[string]any
	Attrs      map[string]any
}

// ErrSkip means the delivery is understood but records nothing (e.g. a completed checkout).
var ErrSkip = errors.New("nothing to record")

type shopCustomer struct {
	ID          json.Number `json:"id"`
	Email       string      `json:"email"`
	Phone       string      `json:"phone"`
	FirstName   string      `json:"first_name"`
	LastName    string      `json:"last_name"`
	OrdersCount *int        `json:"orders_count"`
	TotalSpent  string      `json:"total_spent"`
}

type shopLine struct {
	ProductID json.Number `json:"product_id"`
	VariantID json.Number `json:"variant_id"`
	Title     string      `json:"title"`
	SKU       string      `json:"sku"`
	Quantity  int         `json:"quantity"`
	Price     string      `json:"price"`
}

type shopDoc struct {
	ID                   json.Number   `json:"id"`
	Token                string        `json:"token"`
	Email                string        `json:"email"`
	Phone                string        `json:"phone"`
	Name                 string        `json:"name"`
	OrderNumber          json.Number   `json:"order_number"`
	TotalPrice           string        `json:"total_price"`
	TotalDiscounts       string        `json:"total_discounts"`
	Currency             string        `json:"currency"`
	FinancialStatus      string        `json:"financial_status"`
	CheckoutToken        string        `json:"checkout_token"`
	AbandonedCheckoutURL string        `json:"abandoned_checkout_url"`
	CompletedAt          *string       `json:"completed_at"`
	LineItems            []shopLine    `json:"line_items"`
	Customer             *shopCustomer `json:"customer"`
	// customers/create carries the customer at the top level
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

const maxItems = 50

func money(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}

func items(lines []shopLine) ([]any, int) {
	out := make([]any, 0, min(len(lines), maxItems))
	qty := 0
	for i, l := range lines {
		qty += l.Quantity
		if i >= maxItems {
			continue
		}
		it := map[string]any{"title": l.Title, "quantity": l.Quantity, "price": money(l.Price)}
		if l.ProductID != "" {
			it["product_id"] = l.ProductID.String()
		}
		if l.VariantID != "" {
			it["variant_id"] = l.VariantID.String()
		}
		if l.SKU != "" {
			it["sku"] = l.SKU
		}
		out = append(out, it)
	}
	return out, qty
}

// ParseShopify turns a delivery of topic into an Inbound.
func ParseShopify(topic string, body []byte) (Inbound, error) {
	var d shopDoc
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&d); err != nil {
		return Inbound{}, fmt.Errorf("invalid JSON: %w", err)
	}
	in := Inbound{Attrs: map[string]any{}, Props: map[string]any{"source": "shopify"}}
	cust := d.Customer
	if topic == "customers/create" {
		cust = &shopCustomer{ID: d.ID, Email: d.Email, Phone: d.Phone, FirstName: d.FirstName, LastName: d.LastName}
	}
	in.Email, in.Phone = strings.TrimSpace(d.Email), strings.TrimSpace(d.Phone)
	if cust != nil {
		if in.Email == "" {
			in.Email = strings.TrimSpace(cust.Email)
		}
		if in.Phone == "" {
			in.Phone = strings.TrimSpace(cust.Phone)
		}
		if cust.ID != "" {
			in.CustomerID = cust.ID.String()
			in.Attrs["shopify_customer_id"] = in.CustomerID
		}
		for k, v := range map[string]string{"first_name": cust.FirstName, "last_name": cust.LastName} {
			if strings.TrimSpace(v) != "" {
				in.Attrs[k] = strings.TrimSpace(v)
			}
		}
		if cust.OrdersCount != nil {
			in.Attrs["shopify_orders_count"] = *cust.OrdersCount
		}
		if cust.TotalSpent != "" {
			in.Attrs["shopify_total_spent"] = money(cust.TotalSpent)
		}
	}
	if in.Phone != "" {
		in.Attrs["phone"] = in.Phone
	}
	switch topic {
	case "orders/create":
		its, qty := items(d.LineItems)
		amount := money(d.TotalPrice)
		in.Name = EventOrderCompleted
		for k, v := range map[string]any{"order_id": d.ID.String(), "order_number": d.OrderNumber.String(), "order_name": d.Name,
			"amount": amount, "revenue": amount, "currency": d.Currency, "discount": money(d.TotalDiscounts), "items": its, "item_count": qty,
			"financial_status": d.FinancialStatus, "checkout_token": d.CheckoutToken} {
			if s, ok := v.(string); ok && s == "" {
				continue
			}
			in.Props[k] = v
		}
	case "customers/create":
		in.Name = EventCustomerCreated
		in.Props["customer_id"] = in.CustomerID
	case "checkouts/create", "checkouts/update":
		if d.CompletedAt != nil && *d.CompletedAt != "" {
			return in, fmt.Errorf("%w: the checkout is completed (its order arrives as orders/create)", ErrSkip)
		}
		its, qty := items(d.LineItems)
		in.Name = EventCartUpdated
		token := d.Token
		if token == "" {
			token = d.CheckoutToken
		}
		for k, v := range map[string]any{"checkout_id": d.ID.String(), "checkout_token": token, "amount": money(d.TotalPrice),
			"currency": d.Currency, "items": its, "item_count": qty, "url": d.AbandonedCheckoutURL} {
			if s, ok := v.(string); ok && s == "" {
				continue
			}
			in.Props[k] = v
		}
	default:
		return in, fmt.Errorf("%w: Pulse doesn't use the %q topic", ErrSkip, topic)
	}
	if in.Email == "" && in.Phone == "" && in.CustomerID == "" {
		return in, fmt.Errorf("%w: no email, phone or customer to record it on", ErrSkip)
	}
	return in, nil
}

// SampleShopifyOrder is an orders/create body for previews and tests.
func SampleShopifyOrder(email string) []byte {
	b, _ := json.Marshal(map[string]any{
		"id": 820982911946154508, "email": email, "name": "#9999", "order_number": 1999, "total_price": "1499.00",
		"total_discounts": "100.00", "currency": "INR", "financial_status": "paid", "checkout_token": "sample-checkout",
		"line_items": []any{map[string]any{"product_id": 632910392, "variant_id": 808950810, "title": "Sample T-shirt",
			"sku": "TS-01", "quantity": 2, "price": "799.50"}},
		"customer": map[string]any{"id": 115310627314723954, "email": email, "first_name": "Asha", "last_name": "Rao",
			"orders_count": 3, "total_spent": "4497.00"},
	})
	return b
}

// CheckShopify checks the Admin API token, when there is one.
func CheckShopify(ctx context.Context, c Connector, client *http.Client) error {
	tok := c.Secrets["access_token"]
	if tok == "" {
		return errors.New("add an Admin API access token to test the connection; webhooks work without one")
	}
	base := trimSlash(c.Setting("base_url"))
	if base == "" {
		base = "https://" + strings.ToLower(c.Setting("shop"))
	}
	var resp struct {
		Shop struct {
			Name string `json:"name"`
		} `json:"shop"`
	}
	_, err := caller{client: client, headers: map[string]string{"X-Shopify-Access-Token": tok}}.
		do(ctx, http.MethodGet, base+"/admin/api/"+c.Setting("api_version")+"/shop.json", nil, &resp)
	return err
}

// ShopifyCustomerExternal is the external id of a Shopify customer without email or phone.
func ShopifyCustomerExternal(customerID string) string { return "shopify-" + attr.String(customerID) }

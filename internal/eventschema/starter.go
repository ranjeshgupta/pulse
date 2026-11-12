package eventschema

// Suggested schemas for common e-commerce events, offered by "Add suggested events". They start
// in warn mode, so adding them never refuses an event.

func enum(pairs ...string) []EnumValue {
	out := make([]EnumValue, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, EnumValue{Value: pairs[i], Label: pairs[i+1]})
	}
	return out
}

func productProps() []Prop {
	return []Prop{
		{Key: "product_code", Label: "Product code", Type: TypeString, Description: "Your SKU or product ID"},
		{Key: "product_name", Label: "Product name", Type: TypeString},
		{Key: "product_category", Label: "Product category", Type: TypeString},
	}
}

var paymentMethods = enum("upi", "UPI", "card", "Card", "netbanking", "Net banking", "wallet", "Wallet", "cod", "Cash on delivery", "emi", "EMI", "other", "Other")

func authProps() []Prop {
	return []Prop{
		{Key: "method", Label: "Method", Type: TypeEnum, Required: true, Values: enum("social", "Social", "otp", "OTP", "password", "Password")},
		{Key: "social_provider", Label: "Social provider", Type: TypeEnum, Required: true,
			Values:    enum("google", "Google", "facebook", "Facebook", "apple", "Apple", "linkedin", "LinkedIn"),
			DependsOn: &Dependency{Prop: "method", Values: []string{"social"}}},
	}
}

func abandonedProps() []Prop {
	return append(productProps(),
		Prop{Key: "price", Label: "Price", Type: TypeNumber},
		Prop{Key: "cart_value", Label: "Cart value", Type: TypeNumber},
		Prop{Key: "payment_method", Label: "Payment method", Type: TypeEnum, Values: paymentMethods},
		Prop{Key: "payment_gateway", Label: "Payment gateway", Type: TypeString, Description: "e.g. razorpay, payu"},
		Prop{Key: "failure_reason", Label: "Failure reason", Type: TypeString},
	)
}

// ampProps are what every AMP email answer carries: the block and the message it came from.
func ampProps() []Prop {
	return []Prop{
		{Key: "block", Label: "Block", Type: TypeString, Required: true, Description: "The poll's or form's name in the email"},
		{Key: "template_id", Label: "Template", Type: TypeNumber},
		{Key: "broadcast_id", Label: "Broadcast", Type: TypeNumber},
		{Key: "journey_id", Label: "Journey", Type: TypeNumber},
		{Key: "message_id", Label: "Message", Type: TypeString},
	}
}

// Suggested returns the starter schemas.
func Suggested() []Schema {
	purchase := append(productProps(),
		Prop{Key: "value", Label: "Order value", Type: TypeNumber, Required: true},
		Prop{Key: "currency", Label: "Currency", Type: TypeString, Description: "ISO 4217 code, e.g. INR"},
		Prop{Key: "quantity", Label: "Quantity", Type: TypeNumber},
		Prop{Key: "source", Label: "Source", Type: TypeString, Description: "Where the order came from, e.g. web, app, store"},
		Prop{Key: "affiliation", Label: "Affiliation", Type: TypeString, Description: "Store or partner, e.g. Acme Online"},
		Prop{Key: "order_id", Label: "Order ID", Type: TypeString, Required: true},
		Prop{Key: "items", Label: "Items", Type: TypeList, Description: "One object per product in the order"},
		Prop{Key: "item_product_code", Path: "items[].product_code", Label: "Item product code", Type: TypeString, AddToList: "products"},
		Prop{Key: "item_quantity", Path: "items[].quantity", Label: "Item quantity", Type: TypeNumber},
		Prop{Key: "item_price", Path: "items[].price", Label: "Item price", Type: TypeNumber},
	)
	purchase[0].AddToList = "products"
	return []Schema{
		{Name: "purchase", Label: "Purchase", Description: "An order was paid for.", Mode: ModeWarn, Props: purchase},
		{Name: "signup", Label: "Sign-up", Description: "Someone created an account.", Mode: ModeWarn, Props: authProps()},
		{Name: "login", Label: "Login", Description: "Someone signed in.", Mode: ModeWarn, Props: authProps()},
		{Name: "add_to_cart", Label: "Add to cart", Description: "A product went into the cart.", Mode: ModeWarn, Props: append(productProps(),
			Prop{Key: "price", Label: "Price", Type: TypeNumber},
			Prop{Key: "quantity", Label: "Quantity", Type: TypeNumber},
			Prop{Key: "source", Label: "Source", Type: TypeString, Description: "e.g. web, app"},
			Prop{Key: "landing_page", Label: "Landing page", Type: TypeString, Description: "The page the visit started on"},
		)},
		{Name: "cart_abandoned", Label: "Cart abandoned", Description: "A cart was left without checking out.", Mode: ModeWarn, Props: abandonedProps()},
		{Name: "checkout_abandoned", Label: "Checkout abandoned", Description: "A checkout or payment was started and not finished.", Mode: ModeWarn, Props: abandonedProps()},
		{Name: "amp_poll_answered", Label: "AMP poll answered", Description: "Someone answered a poll inside an AMP email (recorded by Pulse).", Mode: ModeWarn, Props: append(ampProps(),
			Prop{Key: "question", Label: "Question", Type: TypeString},
			Prop{Key: "answer", Label: "Answer", Type: TypeString, Required: true},
		)},
		{Name: "amp_form_submitted", Label: "AMP form submitted", Description: "Someone sent a form inside an AMP email (recorded by Pulse; one property per field).", Mode: ModeWarn, Props: ampProps()},
	}
}

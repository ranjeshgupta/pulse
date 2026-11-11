package bounces

// Webhook verification. The bounce URL holds a per-workspace secret (it alone selects the
// workspace: nothing in a body is trusted for that). On top of it, each service's own scheme is
// checked when the workspace has saved what it needs:
//
//   - SendGrid signed event webhook: X-Twilio-Email-Event-Webhook-Signature, an ECDSA (P-256,
//     SHA-256) signature over timestamp + body, checked with the verification key SendGrid shows;
//   - Mailgun: signature.signature = hex HMAC-SHA256(timestamp + token) with the HTTP webhook
//     signing key (timestamps older than a day are refused);
//   - Amazon SES through SNS: always — the SNS message signature (version 1 SHA1, 2 SHA256) with the
//     certificate at SigningCertURL, which must be on an sns.<region>.amazonaws.com host;
//   - Postmark, SparkPost, generic posts and SES without SNS: HTTP basic auth with the saved
//     user name and password (both services put credentials in the webhook URL).
//
// Once a scheme's key is saved, a body of that format that doesn't pass it is refused.

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Keys are what a workspace saved for verifying its email webhooks (stored sealed).
type Keys struct {
	SendGridKey   string `json:"sendgrid_public_key,omitempty"` // base64 DER or PEM public key
	MailgunKey    string `json:"mailgun_signing_key,omitempty"`
	BasicUser     string `json:"basic_user,omitempty"`
	BasicPassword string `json:"basic_password,omitempty"`
}

// Status says which checks are on, for the settings page.
type Status struct {
	SendGrid  bool   `json:"sendgrid"`
	Mailgun   bool   `json:"mailgun"`
	SES       bool   `json:"ses"` // always: SNS signatures
	Basic     bool   `json:"basic"`
	BasicUser string `json:"basic_user,omitempty"`
}

func (k Keys) Status() Status {
	return Status{SendGrid: k.SendGridKey != "", Mailgun: k.MailgunKey != "", SES: true, Basic: k.BasicPassword != "", BasicUser: k.BasicUser}
}

// Validate checks keys before they are saved.
func (k Keys) Validate() error {
	if k.SendGridKey != "" {
		if _, err := parseECKey(k.SendGridKey); err != nil {
			return errors.New("SendGrid verification key: paste the public key SendGrid shows (base64), " + err.Error())
		}
	}
	if (k.BasicUser == "") != (k.BasicPassword == "") {
		return errors.New("basic auth needs both a user name and a password")
	}
	if len(k.BasicPassword) > 0 && len(k.BasicPassword) < 12 {
		return errors.New("basic auth password: at least 12 characters")
	}
	if strings.ContainsAny(k.BasicUser, ":") {
		return errors.New("basic auth user name can't contain ':'")
	}
	return nil
}

// ErrSignature: a webhook didn't pass its workspace's check.
var ErrSignature = errors.New("webhook signature or credentials invalid")

// CertFetcher downloads an SNS signing certificate (PEM) from a checked SNS URL.
type CertFetcher func(url string) ([]byte, error)

// Verify checks a webhook body of a format against the workspace's keys.
func Verify(format string, h http.Header, body []byte, k Keys, certs CertFetcher, now time.Time) error {
	basicOK := false
	if k.BasicPassword != "" {
		if u, p, ok := (&http.Request{Header: h}).BasicAuth(); ok &&
			subtle.ConstantTimeCompare([]byte(u), []byte(k.BasicUser)) == 1 && subtle.ConstantTimeCompare([]byte(p), []byte(k.BasicPassword)) == 1 {
			basicOK = true
		}
	}
	switch format {
	case FormatSendGrid:
		if k.SendGridKey != "" {
			return verifySendGrid(h, body, k.SendGridKey, now)
		}
	case FormatMailgun:
		if k.MailgunKey != "" {
			return verifyMailgun(body, k.MailgunKey, now)
		}
	case FormatSES:
		return VerifySNS(body, certs)
	}
	if k.BasicPassword != "" && !basicOK {
		switch format {
		case FormatPostmark, FormatSparkPost, FormatGeneric, FormatSESDirect:
			return ErrSignature
		}
	}
	return nil
}

func parseECKey(s string) (*ecdsa.PublicKey, error) {
	s = strings.TrimSpace(s)
	var der []byte
	if blk, _ := pem.Decode([]byte(s)); blk != nil {
		der = blk.Bytes
	} else {
		b, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(s), ""))
		if err != nil {
			return nil, errors.New("not base64")
		}
		der = b
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, errors.New("not a public key")
	}
	ec, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("not an ECDSA key")
	}
	return ec, nil
}

func verifySendGrid(h http.Header, body []byte, key string, now time.Time) error {
	pub, err := parseECKey(key)
	if err != nil {
		return ErrSignature
	}
	ts := h.Get("X-Twilio-Email-Event-Webhook-Timestamp")
	sig, err := base64.StdEncoding.DecodeString(h.Get("X-Twilio-Email-Event-Webhook-Signature"))
	if err != nil || ts == "" || len(sig) == 0 {
		return ErrSignature
	}
	if t, err := strconv.ParseInt(ts, 10, 64); err != nil || now.Sub(time.Unix(t, 0)).Abs() > 24*time.Hour {
		return ErrSignature
	}
	sum := sha256.Sum256(append([]byte(ts), body...))
	if !ecdsa.VerifyASN1(pub, sum[:], sig) {
		return ErrSignature
	}
	return nil
}

func verifyMailgun(body []byte, key string, now time.Time) error {
	var p struct {
		Signature struct {
			Timestamp string `json:"timestamp"`
			Token     string `json:"token"`
			Signature string `json:"signature"`
		} `json:"signature"`
	}
	if json.Unmarshal(body, &p) != nil || p.Signature.Signature == "" || p.Signature.Token == "" {
		return ErrSignature
	}
	if t, err := strconv.ParseInt(p.Signature.Timestamp, 10, 64); err != nil || now.Sub(time.Unix(t, 0)).Abs() > 24*time.Hour {
		return ErrSignature
	}
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(p.Signature.Timestamp + p.Signature.Token))
	if !hmac.Equal([]byte(hex.EncodeToString(m.Sum(nil))), []byte(strings.ToLower(p.Signature.Signature))) {
		return ErrSignature
	}
	return nil
}

// VerifySNS checks an Amazon SNS message's signature with its signing certificate.
func VerifySNS(body []byte, certs CertFetcher) error {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return ErrSignature
	}
	certURL := str(m, "SigningCertURL")
	if certURL == "" {
		certURL = str(m, "SigningCertUrl")
	}
	if certs == nil || !snsURL(certURL) || !strings.HasSuffix(strings.ToLower(strings.SplitN(certURL, "?", 2)[0]), ".pem") {
		return ErrSignature
	}
	var fields []string
	switch str(m, "Type") {
	case "Notification":
		fields = []string{"Message", "MessageId", "Subject", "Timestamp", "TopicArn", "Type"}
	case "SubscriptionConfirmation", "UnsubscribeConfirmation":
		fields = []string{"Message", "MessageId", "SubscribeURL", "Timestamp", "Token", "TopicArn", "Type"}
	default:
		return ErrSignature
	}
	var sb strings.Builder
	for _, f := range fields {
		v, ok := m[f].(string)
		if !ok {
			if f == "Subject" {
				continue
			}
			return ErrSignature
		}
		sb.WriteString(f + "\n" + v + "\n")
	}
	sig, err := base64.StdEncoding.DecodeString(str(m, "Signature"))
	if err != nil {
		return ErrSignature
	}
	pemBytes, err := certs(certURL)
	if err != nil {
		return ErrSignature
	}
	blk, _ := pem.Decode(pemBytes)
	if blk == nil {
		return ErrSignature
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return ErrSignature
	}
	pub, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return ErrSignature
	}
	var hash crypto.Hash
	var sum []byte
	switch str(m, "SignatureVersion") {
	case "1":
		s := sha1.Sum([]byte(sb.String()))
		hash, sum = crypto.SHA1, s[:]
	case "2":
		s := sha256.Sum256([]byte(sb.String()))
		hash, sum = crypto.SHA256, s[:]
	default:
		return ErrSignature
	}
	if rsa.VerifyPKCS1v15(pub, hash, sum, sig) != nil {
		return ErrSignature
	}
	return nil
}

// MailgunToken is the signature token of a Mailgun webhook. Mailgun signs only the timestamp and
// this token (not the body), so a verified token must be used once: the caller remembers it.
func MailgunToken(body []byte) string {
	var p struct {
		Signature struct {
			Token string `json:"token"`
		} `json:"signature"`
	}
	_ = json.Unmarshal(body, &p)
	return p.Signature.Token
}

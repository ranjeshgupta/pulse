package bounces

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestVerifySendGrid(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	keys := Keys{SendGridKey: base64.StdEncoding.EncodeToString(der)}
	if err := keys.Validate(); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	body := []byte(`[{"email":"a@x.co","event":"delivered"}]`)
	sign := func(ts string, b []byte) http.Header {
		sum := sha256.Sum256(append([]byte(ts), b...))
		sig, _ := ecdsa.SignASN1(rand.Reader, priv, sum[:])
		return http.Header{"X-Twilio-Email-Event-Webhook-Signature": {base64.StdEncoding.EncodeToString(sig)},
			"X-Twilio-Email-Event-Webhook-Timestamp": {ts}}
	}
	ts := strconv.FormatInt(now.Unix(), 10)
	if err := Verify(FormatSendGrid, sign(ts, body), body, keys, nil, now); err != nil {
		t.Errorf("valid signature refused: %v", err)
	}
	if err := Verify(FormatSendGrid, sign(ts, body), []byte(`[{"email":"evil@x.co","event":"bounce"}]`), keys, nil, now); !errors.Is(err, ErrSignature) {
		t.Error("a changed body was accepted")
	}
	if err := Verify(FormatSendGrid, http.Header{}, body, keys, nil, now); !errors.Is(err, ErrSignature) {
		t.Error("an unsigned body was accepted once the key is saved")
	}
	old := strconv.FormatInt(now.Add(-48*time.Hour).Unix(), 10)
	if err := Verify(FormatSendGrid, sign(old, body), body, keys, nil, now); !errors.Is(err, ErrSignature) {
		t.Error("an old signature was accepted")
	}
	if err := Verify(FormatSendGrid, http.Header{}, body, Keys{}, nil, now); err != nil {
		t.Error("without a key the secret URL is the check")
	}
	if (Keys{SendGridKey: "bm90IGEga2V5"}).Validate() == nil {
		t.Error("a bad key was accepted")
	}
}

func TestVerifyMailgun(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	mk := func(key, ts string) []byte {
		m := hmac.New(sha256.New, []byte(key))
		m.Write([]byte(ts + "tok123"))
		b, _ := json.Marshal(map[string]any{"signature": map[string]string{"timestamp": ts, "token": "tok123", "signature": hex.EncodeToString(m.Sum(nil))},
			"event-data": map[string]any{"event": "delivered", "recipient": "a@x.co"}})
		return b
	}
	keys := Keys{MailgunKey: "key-abc"}
	ts := strconv.FormatInt(now.Unix(), 10)
	if err := Verify(FormatMailgun, nil, mk("key-abc", ts), keys, nil, now); err != nil {
		t.Errorf("valid: %v", err)
	}
	if err := Verify(FormatMailgun, nil, mk("wrong", ts), keys, nil, now); !errors.Is(err, ErrSignature) {
		t.Error("wrong key accepted")
	}
	if err := Verify(FormatMailgun, nil, mk("key-abc", strconv.FormatInt(now.Add(-25*time.Hour).Unix(), 10)), keys, nil, now); !errors.Is(err, ErrSignature) {
		t.Error("old timestamp accepted")
	}
}

func TestVerifyBasicAuth(t *testing.T) {
	keys := Keys{BasicUser: "pulse", BasicPassword: "a-long-password"}
	if err := keys.Validate(); err != nil {
		t.Fatal(err)
	}
	r, _ := http.NewRequest("POST", "/", nil)
	r.SetBasicAuth("pulse", "a-long-password")
	for _, f := range []string{FormatPostmark, FormatSparkPost, FormatGeneric, FormatSESDirect} {
		if err := Verify(f, r.Header, []byte(`{}`), keys, nil, time.Now()); err != nil {
			t.Errorf("%s with credentials: %v", f, err)
		}
		if err := Verify(f, http.Header{}, []byte(`{}`), keys, nil, time.Now()); !errors.Is(err, ErrSignature) {
			t.Errorf("%s without credentials was accepted", f)
		}
	}
	if (Keys{BasicUser: "x", BasicPassword: "short"}).Validate() == nil || (Keys{BasicUser: "x"}).Validate() == nil {
		t.Error("weak or half basic auth accepted")
	}
}

func TestVerifySNS(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "sns.amazonaws.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	const certURL = "https://sns.ap-south-1.amazonaws.com/SimpleNotificationService-abc.pem"
	fetched := ""
	fetch := func(u string) ([]byte, error) { fetched = u; return certPEM, nil }
	msg := map[string]string{"Type": "Notification", "MessageId": "m1", "TopicArn": "arn:aws:sns:ap-south-1:1:ses",
		"Message": `{"notificationType":"Delivery"}`, "Timestamp": "2026-11-11T10:00:00.000Z", "SignatureVersion": "2", "SigningCertURL": certURL}
	toSign := "Message\n" + msg["Message"] + "\nMessageId\nm1\nTimestamp\n" + msg["Timestamp"] + "\nTopicArn\n" + msg["TopicArn"] + "\nType\nNotification\n"
	sum := sha256.Sum256([]byte(toSign))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	msg["Signature"] = base64.StdEncoding.EncodeToString(sig)
	body, _ := json.Marshal(msg)
	if err := Verify(FormatSES, nil, body, Keys{}, fetch, time.Now()); err != nil || fetched != certURL {
		t.Fatalf("valid SNS message: %v (fetched %q)", err, fetched)
	}
	msg["Message"] = `{"notificationType":"Bounce"}`
	body, _ = json.Marshal(msg)
	if err := Verify(FormatSES, nil, body, Keys{}, fetch, time.Now()); !errors.Is(err, ErrSignature) {
		t.Error("a changed SNS message was accepted")
	}
	msg["SigningCertURL"] = "https://evil.test/cert.pem"
	body, _ = json.Marshal(msg)
	fetched = ""
	if err := Verify(FormatSES, nil, body, Keys{}, fetch, time.Now()); !errors.Is(err, ErrSignature) || fetched != "" {
		t.Error("a certificate outside SNS was fetched")
	}
}

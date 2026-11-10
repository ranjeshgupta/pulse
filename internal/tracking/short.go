package tracking

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Short tracked links for text messages (SMS, RCS), where every character counts: a link becomes
// {base}/t/s/{code}, where code is 10 random base62 characters naming the message plus one naming
// the link. What the code stands for (the message's token and its destinations) is kept in Redis
// for ShortLinkTTL, one key per message.

// ShortLinkTTL is how long short links keep working (and recording clicks).
const ShortLinkTTL = 30 * 24 * time.Hour

const (
	base62    = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	codeLen   = 10
	maxShort  = 20 // links per message
	shortPath = "/t/s/"
)

// ShortLink is what a message's short code stands for.
type ShortLink struct {
	Token Token    `json:"t"`
	URLs  []string `json:"u"`
}

var textURL = regexp.MustCompile(`https?://[^\s<>"']+`)

// ShortenText replaces http(s) links in text with short links under base. Links already pointing at
// base's tracking or unsubscribe paths stay as they are. It returns the new text and the original
// destinations, in the order of their link index.
func ShortenText(text, base, code string, urls []string) (string, []string) {
	out := textURL.ReplaceAllStringFunc(text, func(m string) string {
		dest := strings.TrimRight(m, ".,;:!?)]}")
		tail := m[len(dest):]
		if strings.HasPrefix(dest, base+"/t/") || strings.HasPrefix(dest, base+"/u/") {
			return m
		}
		i := indexOf(urls, dest)
		if i < 0 {
			if len(urls) >= maxShort {
				return m
			}
			urls = append(urls, dest)
			i = len(urls) - 1
		}
		return ShortURL(base, code, i) + tail
	})
	return out, urls
}

// ShortURL is the short link for a message's i-th destination.
func ShortURL(base, code string, i int) string { return base + shortPath + code + string(base62[i]) }

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

// NewShortCode returns a random message code.
func NewShortCode() string {
	b := make([]byte, codeLen)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = base62[int(b[i])%len(base62)]
	}
	return string(b)
}

func shortKey(code string) string { return "slink:" + code }

// SaveShort stores what a message's code stands for.
func SaveShort(ctx context.Context, rdb redis.Cmdable, code string, l ShortLink) error {
	b, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return rdb.Set(ctx, shortKey(code), b, ShortLinkTTL).Err()
}

// ErrNoShortLink: the code is malformed, unknown or expired.
var ErrNoShortLink = errors.New("unknown or expired link")

// ResolveShort returns the message token and the destination a short link's code (message code
// plus link index) points at.
func ResolveShort(ctx context.Context, rdb redis.Cmdable, full string) (Token, string, error) {
	if len(full) != codeLen+1 {
		return Token{}, "", ErrNoShortLink
	}
	i := strings.IndexByte(base62, full[codeLen])
	for _, r := range full {
		if !strings.ContainsRune(base62, r) {
			return Token{}, "", ErrNoShortLink
		}
	}
	raw, err := rdb.Get(ctx, shortKey(full[:codeLen])).Bytes()
	if errors.Is(err, redis.Nil) {
		return Token{}, "", ErrNoShortLink
	} else if err != nil {
		return Token{}, "", err
	}
	var l ShortLink
	if err := json.Unmarshal(raw, &l); err != nil || i < 0 || i >= len(l.URLs) {
		return Token{}, "", ErrNoShortLink
	}
	return l.Token, l.URLs[i], nil
}

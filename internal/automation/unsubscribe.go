package automation

// Unsubscribe settings (Settings → Bounces & unsubscribes, the "unsubscribe" section): what the
// unsubscribe link in an email does, whether email unsubscribes stop email by themselves, and the
// workspace's subscription topics (product or list unsubscribes).

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"pulse/internal/channel"
)

// Link modes.
const (
	LinkConfirm  = "confirm"   // the link opens a page where the person confirms (and can pick topics)
	LinkOneClick = "one_click" // the link unsubscribes at once (the page confirms it was done)
)

// Email unsubscribe policies.
const (
	// EmailEnforce: an email unsubscribe (or Do Not Contact) stops email to the contact by itself.
	EmailEnforce = "enforce"
	// EmailSegment: unsubscribed contacts stay sendable; campaigns leave them out with a segment
	// filter. Contacts with an email spam complaint or hard bounce are never emailed.
	EmailSegment = "segment"
)

// Topic is a subscription topic: a product, newsletter or list people can leave on its own.
type Topic struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Unsubscribe is the section.
type Unsubscribe struct {
	LinkMode    string  `json:"link_mode"`
	EmailPolicy string  `json:"email_policy"`
	Topics      []Topic `json:"topics"`
}

// DefaultUnsubscribe is what Pulse did before: a confirmation page, email unsubscribes enforced,
// no topics.
func DefaultUnsubscribe() Unsubscribe {
	return Unsubscribe{LinkMode: LinkConfirm, EmailPolicy: EmailEnforce, Topics: []Topic{}}
}

// MaxTopics caps a workspace's topics.
const MaxTopics = 30

var topicKeyRe = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

// ValidTopicKey reports whether k can be a topic key (lowercase letters, digits and _).
func ValidTopicKey(k string) bool { return topicKeyRe.MatchString(k) }

// Topic returns the topic with key k.
func (u Unsubscribe) Topic(k string) (Topic, bool) {
	for _, t := range u.Topics {
		if t.Key == k {
			return t, true
		}
	}
	return Topic{}, false
}

func validateUnsubscribe(u *Unsubscribe) error {
	if u.LinkMode != LinkConfirm && u.LinkMode != LinkOneClick {
		return errors.New("unsubscribe.link_mode must be confirm or one_click")
	}
	if u.EmailPolicy != EmailEnforce && u.EmailPolicy != EmailSegment {
		return errors.New("unsubscribe.email_policy must be enforce or segment")
	}
	if len(u.Topics) > MaxTopics {
		return fmt.Errorf("at most %d topics", MaxTopics)
	}
	seen := map[string]bool{}
	for i := range u.Topics {
		t := &u.Topics[i]
		t.Name, t.Description = strings.TrimSpace(t.Name), strings.TrimSpace(t.Description)
		switch {
		case !ValidTopicKey(t.Key):
			return fmt.Errorf("topic %q: the key takes lowercase letters, digits and _ (at most 40)", t.Key)
		case t.Key == "all" || channel.Valid(t.Key):
			return fmt.Errorf("topic %q: pick a key that isn't a channel name", t.Key)
		case seen[t.Key]:
			return fmt.Errorf("topic %q is listed twice", t.Key)
		case t.Name == "" || len(t.Name) > 80:
			return fmt.Errorf("topic %q: give it a name of at most 80 characters", t.Key)
		case len(t.Description) > 300:
			return fmt.Errorf("topic %q: the description is too long", t.Key)
		}
		seen[t.Key] = true
	}
	if u.Topics == nil {
		u.Topics = []Topic{}
	}
	return nil
}

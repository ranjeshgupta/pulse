package profile

import (
	"strings"

	"github.com/nyaruka/phonenumbers"
)

// PhoneCountry returns the ISO 3166 country code (e.g. IN) of a phone number written in
// international form (+91…, or 00 91…), or "" when it has none: national numbers without a
// country code can't be placed.
func PhoneCountry(phone string) string {
	p := strings.TrimSpace(phone)
	if strings.HasPrefix(p, "00") {
		p = "+" + p[2:]
	}
	if !strings.HasPrefix(p, "+") || len(p) > 40 {
		return ""
	}
	num, err := phonenumbers.Parse(p, "")
	if err != nil {
		return ""
	}
	region := phonenumbers.GetRegionCodeForNumber(num)
	if region == "" || region == "ZZ" {
		// Not a valid number for any one region: fall back to the code's main region (+1 → US).
		region = phonenumbers.GetRegionCodeForCountryCode(int(num.GetCountryCode()))
	}
	if region == "ZZ" || region == "001" {
		return ""
	}
	return region
}

package segment

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	r, err := Parse([]byte(`{"all":[
		{"attr":"city","op":"eq","value":"Delhi"},
		{"event":"purchase","op":"gte","count":3,"days":7},
		{"any":[{"attr":"plan","op":"in","value":["pro","team"]},{"event":"login","op":"eq","count":0,"days":30}]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	events, attrs := r.Refs()
	if want := []string{"login", "purchase"}; !reflect.DeepEqual(events, want) {
		t.Errorf("event refs = %v, want %v", events, want)
	}
	if want := []string{"city", "plan"}; !reflect.DeepEqual(attrs, want) {
		t.Errorf("attr refs = %v, want %v", attrs, want)
	}
	if !r.HasWindow() {
		t.Error("HasWindow = false, want true")
	}
}

func TestParseInvalid(t *testing.T) {
	cases := map[string]struct{ json, wantErr string }{
		"two kinds":         {`{"attr":"a","event":"b","op":"eq","value":1}`, "exactly one"},
		"empty group":       {`{"all":[]}`, "at least one"},
		"group with op":     {`{"all":[{"attr":"a","op":"exists"}],"op":"eq"}`, "cannot have"},
		"bad attr name":     {`{"attr":"a-b","op":"exists"}`, "invalid attribute"},
		"sql in attr name":  {`{"attr":"x'] OR 1=1 --","op":"exists"}`, "invalid attribute"},
		"numeric op string": {`{"attr":"age","op":"gt","value":"30"}`, "number value"},
		"contains number":   {`{"attr":"a","op":"contains","value":3}`, "string value"},
		"empty in":          {`{"attr":"a","op":"in","value":[]}`, "list of"},
		"in with object":    {`{"attr":"a","op":"in","value":[{"x":1}]}`, "in values"},
		"exists with value": {`{"attr":"a","op":"exists","value":1}`, "takes no value"},
		"unknown attr op":   {`{"attr":"a","op":"like","value":"x"}`, "unknown op"},
		"event no count":    {`{"event":"buy","op":"gte","days":7}`, "count"},
		"event no days":     {`{"event":"buy","op":"gte","count":1}`, "days"},
		"event too long":    {`{"event":"buy","op":"gte","count":1,"days":400}`, "days"},
		"event with value":  {`{"event":"buy","op":"gte","count":1,"days":1,"value":2}`, "use count"},
		"attr with days":    {`{"attr":"a","op":"exists","days":3}`, "event rules only"},
		"unknown field":     {`{"attr":"a","op":"exists","foo":1}`, "unknown field"},
		"bad event name":    {`{"event":"1buy","op":"gte","count":1,"days":1}`, "invalid event"},
		"negative count":    {`{"event":"buy","op":"gte","count":-1,"days":1}`, "count"},
		"unknown event op":  {`{"event":"buy","op":"in","count":1,"days":1}`, "unknown op"},
		"segment bad op":    {`{"segment":3,"op":"eq"}`, "in or not_in"},
		"segment value":     {`{"segment":3,"op":"in","value":1}`, "no value"},
		"tag bad name":      {`{"tag":"a b","op":"has"}`, "tag rules"},
		"tag bad op":        {`{"tag":"vip","op":"eq"}`, "tag rules"},
		"where on attr":     {`{"attr":"a","op":"exists","where":[{"prop":"x","op":"exists"}]}`, "event rules only"},
		"where bad prop":    {`{"event":"buy","op":"gte","count":1,"days":1,"where":[{"prop":"a..b","op":"exists"}]}`, "invalid property"},
		"where bad op":      {`{"event":"buy","op":"gte","count":1,"days":1,"where":[{"prop":"a","op":"gt","value":"x"}]}`, "number value"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.json))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestValidateDepthLimit(t *testing.T) {
	r := Rule{Attr: "a", Op: "exists"}
	for i := 0; i < maxDepth; i++ {
		r = Rule{All: []Rule{r}}
	}
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "nested") {
		t.Fatalf("err = %v, want depth error", err)
	}
}

func TestAttrOnlyRuleHasNoWindow(t *testing.T) {
	r, err := Parse([]byte(`{"attr":"city","op":"eq","value":"Pune"}`))
	if err != nil {
		t.Fatal(err)
	}
	if r.HasWindow() {
		t.Error("HasWindow = true for attribute-only rule")
	}
}

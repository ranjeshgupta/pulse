package importer

import (
	"context"
	"strings"
	"testing"
)

// FuzzRun imports arbitrary CSV files: nothing may panic, and the result never counts more rows
// imported than the file has lines.
func FuzzRun(f *testing.F) {
	f.Add("external_id,email,city,tags,score\nu1,a@b.co,Pune,\"vip,beta\",12\nu2,,\"two\nlines\",,x\n", "number")
	f.Add("\ufeffEmail,First Name\nA@B.CO,Asha\n", "")
	f.Add("id,bad col\n1,2\n", "")
	f.Fuzz(func(t *testing.T, src, scoreType string) {
		types := map[string]string{"score": scoreType, "tags": "list", "city": "string"}
		res, _ := Run(context.Background(), &nopPub{}, strings.NewReader(src), Options{Tenant: 1, ImportID: "f", Types: types, MaxRows: 1000})
		if res.Imported > int64(strings.Count(src, "\n")+1) {
			t.Fatalf("imported %d rows from %d lines", res.Imported, strings.Count(src, "\n")+1)
		}
	})
}

package importer

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

type nopPub struct{ n int }

func (p *nopPub) Publish(_ context.Context, recs ...*kgo.Record) error { p.n += len(recs); return nil }

func TestRunStopsOnHugeLines(t *testing.T) {
	src := "external_id,city\nu1,Pune\n" + strings.Repeat("x", MaxLine+10)
	res, err := Run(context.Background(), &nopPub{}, strings.NewReader(src), Options{Tenant: 1, ImportID: "t"})
	if !errors.Is(err, ErrLineTooLong) || res.Imported != 1 {
		t.Fatalf("err = %v, imported %d", err, res.Imported)
	}
}

func TestRunImportsAndReportsBadRows(t *testing.T) {
	pub := &nopPub{}
	src := "email,age\na@x.co,30\nb@x.co,old\n,40\n"
	res, err := Run(context.Background(), pub, strings.NewReader(src), Options{Tenant: 1, ImportID: "t", ListID: 5,
		Types: map[string]string{"age": "number"}})
	if err != nil || res.Rows != 3 || res.Imported != 1 || len(res.Errors) != 2 || pub.n != 2 {
		t.Fatalf("res = %+v, err %v, published %d", res, err, pub.n)
	}
}

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

func TestRunStopsOnHugeQuotedFields(t *testing.T) {
	src := "external_id,note\nu1,\"" + strings.Repeat("line\n", MaxLine/5+10) + "\"\n"
	if _, err := Run(context.Background(), &nopPub{}, strings.NewReader(src), Options{Tenant: 1, ImportID: "t"}); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("err = %v, want ErrLineTooLong for a quoted field full of line breaks", err)
	}
	ok := "external_id,note\nu1,\"two\nlines\"\nu2,\"say \"\"hi\"\"\"\n"
	if res, err := Run(context.Background(), &nopPub{}, strings.NewReader(ok), Options{Tenant: 1, ImportID: "t"}); err != nil || res.Imported != 2 {
		t.Fatalf("normal quoted fields: %+v %v", res, err)
	}
}

func TestRunStopsOnMalformedQuotes(t *testing.T) {
	src := "external_id,note\nu1,ok\nu2,a\"b\nu3," + strings.Repeat("x\n", 10)
	res, err := Run(context.Background(), &nopPub{}, strings.NewReader(src), Options{Tenant: 1, ImportID: "t"})
	if !errors.Is(err, ErrMalformed) || res.Imported != 1 || len(res.Errors) != 1 || res.Errors[0].Row != 3 {
		t.Fatalf("res %+v err %v, want a stop at row 3", res, err)
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

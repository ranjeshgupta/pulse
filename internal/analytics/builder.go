package analytics

// Custom reports (a small Mautic-style report builder): a data source, a metric, an optional group
// and filters, compiled to one bounded, parameterized query. At most MaxGroups groups come back,
// date ranges are a year at most, and grouping by day covers at most MaxGroups days.

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"pulse/internal/attr"
	"pulse/internal/segment"
)

const (
	MaxGroups       = 100
	maxReportEvents = 10

	SourceEvents   = "events"
	SourceMessages = "messages"
	SourceContacts = "contacts"

	MetricCount          = "count"           // events or messages (contacts: contacts)
	MetricUniqueContacts = "unique_contacts" // distinct contacts
	MetricSum            = "sum"             // of a numeric property (events) or attribute (contacts)
	MetricAvg            = "avg"

	GroupNone     = "none"
	GroupEvent    = "event"    // events: event name
	GroupProp     = "prop"     // events: an event property
	GroupAttr     = "attr"     // events, contacts: a profile attribute (contacts snapshot)
	GroupKind     = "kind"     // messages: sent, opened, clicked, ...
	GroupChannel  = "channel"  // messages
	GroupCampaign = "campaign" // messages: the broadcast or journey
	GroupDay      = "day"      // time: events and messages by ts, contacts by first seen
	GroupWeek     = "week"
	GroupMonth    = "month"

	ChartTable = "table"
	ChartBars  = "bars"
	ChartLine  = "line"
)

var (
	messageKinds    = []string{"sent", "delivered", "read", "failed", "fallback", "suppressed", "capped", "deferred", "opened", "clicked", "bounced", "unsubscribed"}
	messageChannels = []string{"email", "sms", "whatsapp", "rcs", "push", "inbox"}
)

// ReportSpec defines a custom report.
type ReportSpec struct {
	Source  string  `json:"source"`
	Metric  Metric  `json:"metric"`
	GroupBy GroupBy `json:"group_by"`
	// Events (events source): only these event names; empty means every event.
	Events []string `json:"events,omitempty"`
	// Where: event property filters (events) or profile attribute filters (contacts), as in segment rules.
	Where []segment.PropFilter `json:"where,omitempty"`
	// Messages source: limit to broadcasts or journeys (empty: both), channels and outcome kinds.
	MessageSource string   `json:"message_source,omitempty"`
	Channels      []string `json:"channels,omitempty"`
	Kinds         []string `json:"kinds,omitempty"`
	SegmentID     int64    `json:"segment_id,omitempty"`
	Chart         string   `json:"chart"`
	// RangeDays is the default date range (the last N days) when a run gives none.
	RangeDays int `json:"range_days,omitempty"`
}

type Metric struct {
	Type string `json:"type"`
	Key  string `json:"key,omitempty"` // sum, avg: the property or attribute
}

type GroupBy struct {
	Type string `json:"type"`
	Key  string `json:"key,omitempty"` // prop, attr
}

// TimeGroup reports whether the report is grouped by a period.
func (s ReportSpec) TimeGroup() (Period, bool) {
	switch s.GroupBy.Type {
	case GroupDay:
		return Day, true
	case GroupWeek:
		return Week, true
	case GroupMonth:
		return Month, true
	}
	return "", false
}

func (s ReportSpec) Validate() error {
	allowedGroups := map[string][]string{
		SourceEvents:   {GroupNone, GroupEvent, GroupProp, GroupAttr, GroupDay, GroupWeek, GroupMonth},
		SourceMessages: {GroupNone, GroupKind, GroupChannel, GroupCampaign, GroupDay, GroupWeek, GroupMonth},
		SourceContacts: {GroupNone, GroupAttr, GroupDay, GroupWeek, GroupMonth},
	}
	groups, ok := allowedGroups[s.Source]
	if !ok {
		return errors.New("choose a data source: events, message outcomes or contacts")
	}
	switch s.Metric.Type {
	case MetricCount, MetricUniqueContacts:
		if s.Metric.Key != "" {
			return errors.New("this metric takes no property")
		}
	case MetricSum, MetricAvg:
		if s.Source == SourceMessages {
			return errors.New("message outcomes can be counted, not summed")
		}
		if !attr.ValidKey(s.Metric.Key) {
			return errors.New("enter the numeric property to add up, e.g. amount")
		}
	default:
		return errors.New("choose a metric: count, unique contacts, sum or average")
	}
	if !slices.Contains(groups, s.GroupBy.Type) {
		return fmt.Errorf("a %s report can't be grouped by %q", s.Source, s.GroupBy.Type)
	}
	if s.GroupBy.Type == GroupProp || s.GroupBy.Type == GroupAttr {
		if !attr.ValidKey(s.GroupBy.Key) {
			return errors.New("enter the property or attribute to group by")
		}
	} else if s.GroupBy.Key != "" {
		return errors.New("only property and attribute groups take a key")
	}
	switch s.Source {
	case SourceEvents:
		if err := validEvents("report", s.Events, maxReportEvents); err != nil {
			return err
		}
		if err := segment.ValidatePropFilters("report", s.Where); err != nil {
			return err
		}
	case SourceContacts:
		if len(s.Events) > 0 {
			return errors.New("contacts reports take no events")
		}
		if err := segment.ValidateAttrFilters("report", s.Where); err != nil {
			return err
		}
	}
	if s.Source != SourceEvents && s.Source != SourceContacts && len(s.Where) > 0 {
		return errors.New("message reports filter by channel and outcome, not properties")
	}
	if s.Source != SourceMessages && (s.MessageSource != "" || len(s.Channels) > 0 || len(s.Kinds) > 0) {
		return errors.New("channels and outcomes apply to message reports only")
	}
	if s.MessageSource != "" && s.MessageSource != "broadcast" && s.MessageSource != "journey" {
		return errors.New("messages come from broadcasts or journeys")
	}
	for _, c := range s.Channels {
		if !slices.Contains(messageChannels, c) {
			return fmt.Errorf("unknown channel %q", c)
		}
	}
	for _, k := range s.Kinds {
		if !slices.Contains(messageKinds, k) {
			return fmt.Errorf("unknown outcome %q", k)
		}
	}
	if len(s.Channels) > len(messageChannels) || len(s.Kinds) > len(messageKinds) {
		return errors.New("too many filters")
	}
	if s.SegmentID < 0 {
		return errors.New("invalid segment")
	}
	switch s.Chart {
	case ChartTable, ChartBars:
	case ChartLine:
		if _, ok := s.TimeGroup(); !ok {
			return errors.New("a line chart needs a group by day, week or month")
		}
	default:
		return errors.New("show it as a table, bars or a line")
	}
	if s.RangeDays < 0 || s.RangeDays > 366 {
		return errors.New("the default range is up to 366 days")
	}
	return nil
}

// ReportSQL compiles a report over [from, to). It returns (group String, value Float64) rows: time
// groups in time order, other groups by value, largest first; MaxGroups at most.
func ReportSQL(tenantID int64, s ReportSpec, from, to time.Time) (Query, error) {
	if err := s.Validate(); err != nil {
		return Query{}, err
	}
	if err := CheckRange(from, to); err != nil {
		return Query{}, err
	}
	if s.GroupBy.Type == GroupDay && to.Sub(from) > MaxGroups*24*time.Hour {
		return Query{}, fmt.Errorf("grouping by day covers up to %d days; group by week or month for longer ranges", MaxGroups)
	}
	t := uint64(tenantID)
	from, to = from.UTC(), to.UTC()
	switch s.Source {
	case SourceMessages:
		return messagesReport(t, s, from, to), nil
	case SourceContacts:
		return contactsReport(t, s, from, to), nil
	}
	return eventsReport(t, s, from, to), nil
}

var limit = " LIMIT " + strconv.Itoa(MaxGroups)

func order(s ReportSpec) string {
	if _, ok := s.TimeGroup(); ok {
		return " ORDER BY g"
	}
	return " ORDER BY v DESC, g"
}

func eventsReport(t uint64, s ReportSpec, from, to time.Time) Query {
	where := " WHERE tenant_id = ?"
	args := []any{t}
	if len(s.Events) > 0 {
		where += " AND name IN (" + marks(len(s.Events)) + ")"
		for _, e := range s.Events {
			args = append(args, e)
		}
	}
	where += " AND ts >= ? AND ts < ?"
	args = append(args, from, to)
	f, fargs := segment.PropFilterSQL(s.Where)
	where += f
	args = append(args, fargs...)
	seg, sargs := segmentScope("contact_id", t, s.SegmentID)
	where += seg
	args = append(args, sargs...)

	// num is the metric's property as a number (NULL when missing or not numeric).
	num, nargs := "", []any(nil)
	if s.Metric.Type == MetricSum || s.Metric.Type == MetricAvg {
		v, vargs := segment.PropValueSQL(s.Metric.Key)
		num, nargs = "toFloat64OrNull("+v+")", vargs
	}

	if s.GroupBy.Type == GroupAttr {
		// Aggregate per contact first, then join the (streamed) contacts snapshot for the attribute.
		var inner, outer string
		var iargs []any
		switch s.Metric.Type {
		case MetricCount:
			inner, outer = "uniq(id) AS n", "toFloat64(sum(e.n))"
		case MetricUniqueContacts:
			inner, outer = "1 AS n", "toFloat64(count())"
		case MetricSum:
			inner, outer, iargs = "sum("+num+") AS s", "toFloat64(ifNull(sum(e.s), 0))", nargs
		case MetricAvg:
			inner, outer = "sum("+num+") AS s, count("+num+") AS n", "ifNull(sum(e.s) / nullIf(sum(e.n), 0), 0)"
			iargs = append(append([]any{}, nargs...), nargs...)
		}
		qargs := append([]any{s.GroupBy.Key, t}, iargs...)
		qargs = append(qargs, args...)
		return Query{
			SQL: "SELECT c.v AS g, " + outer + " AS v FROM (SELECT id, attrs[?] AS v FROM contacts FINAL WHERE tenant_id = ?) AS c" +
				" INNER JOIN (SELECT contact_id, " + inner + " FROM events" + where + " GROUP BY contact_id) AS e ON e.contact_id = c.id" +
				" GROUP BY g" + order(s) + limit,
			Args: qargs,
		}
	}

	var metric string
	var margs []any
	switch s.Metric.Type {
	case MetricCount:
		metric = "toFloat64(uniq(id))"
	case MetricUniqueContacts:
		metric = "toFloat64(uniq(contact_id))"
	case MetricSum:
		metric, margs = "toFloat64(ifNull(sum("+num+"), 0))", nargs
	case MetricAvg:
		metric, margs = "toFloat64(ifNull(avg("+num+"), 0))", nargs
	}
	group, gargs := "''", []any(nil)
	switch s.GroupBy.Type {
	case GroupEvent:
		group = "toString(name)"
	case GroupProp:
		group, gargs = segment.PropValueSQL(s.GroupBy.Key)
	default:
		if p, ok := s.TimeGroup(); ok {
			group = "toString(" + p.trunc("ts") + ")"
		}
	}
	qargs := append(append(append([]any{}, gargs...), margs...), args...)
	return Query{SQL: "SELECT " + group + " AS g, " + metric + " AS v FROM events" + where + " GROUP BY g" + order(s) + limit, Args: qargs}
}

func messagesReport(t uint64, s ReportSpec, from, to time.Time) Query {
	where := " WHERE tenant_id = ?"
	args := []any{t}
	if s.MessageSource != "" {
		where += " AND source = ?"
		args = append(args, s.MessageSource)
	} else {
		where += " AND source IN ('broadcast', 'journey')" // test sends are left out
	}
	if len(s.Kinds) > 0 {
		where += " AND kind IN (" + marks(len(s.Kinds)) + ")"
		for _, k := range s.Kinds {
			args = append(args, k)
		}
	}
	if len(s.Channels) > 0 {
		where += " AND channel IN (" + marks(len(s.Channels)) + ")"
		for _, c := range s.Channels {
			args = append(args, c)
		}
	}
	where += " AND ts >= ? AND ts < ?"
	args = append(args, from, to)
	seg, sargs := segmentScope("contact_id", t, s.SegmentID)
	where += seg
	args = append(args, sargs...)
	metric := "toFloat64(uniq(id))"
	if s.Metric.Type == MetricUniqueContacts {
		metric = "toFloat64(uniq(contact_id))"
	}
	group := "''"
	switch s.GroupBy.Type {
	case GroupKind:
		group = "toString(kind)"
	case GroupChannel:
		group = "toString(channel)"
	case GroupCampaign:
		group = "concat(source, '|', toString(source_id))"
	default:
		if p, ok := s.TimeGroup(); ok {
			group = "toString(" + p.trunc("ts") + ")"
		}
	}
	return Query{SQL: "SELECT " + group + " AS g, " + metric + " AS v FROM message_events" + where + " GROUP BY g" + order(s) + limit, Args: args}
}

func contactsReport(t uint64, s ReportSpec, from, to time.Time) Query {
	const firstSeen = "parseDateTime64BestEffortOrNull(attrs['first_seen'], 3, 'UTC')"
	where := " WHERE tenant_id = ? AND NOT mapContains(attrs, 'merged_into')"
	args := []any{t}
	f, fargs := segment.AttrFilterSQL(s.Where, to)
	where += f
	args = append(args, fargs...)
	seg, sargs := segmentScope("id", t, s.SegmentID)
	where += seg
	args = append(args, sargs...)
	p, timeGroup := s.TimeGroup()
	if timeGroup {
		// Grouped by when contacts were first seen: the range applies to first seen.
		where += " AND " + firstSeen + " >= ? AND " + firstSeen + " < ?"
		args = append(args, from, to)
	}
	metric, margs := "toFloat64(count())", []any(nil)
	switch s.Metric.Type {
	case MetricSum:
		metric, margs = "toFloat64(ifNull(sum(toFloat64OrNull(attrs[?])), 0))", []any{s.Metric.Key}
	case MetricAvg:
		metric, margs = "toFloat64(ifNull(avg(toFloat64OrNull(attrs[?])), 0))", []any{s.Metric.Key}
	}
	group, gargs := "''", []any(nil)
	if s.GroupBy.Type == GroupAttr {
		group, gargs = "attrs[?]", []any{s.GroupBy.Key}
	} else if timeGroup {
		group = "toString(" + p.trunc(firstSeen) + ")"
	}
	qargs := append(append(append([]any{}, gargs...), margs...), args...)
	return Query{SQL: "SELECT " + group + " AS g, " + metric + " AS v FROM contacts FINAL" + where + " GROUP BY g" + order(s) + limit, Args: qargs}
}

// ReportRow is one group's value.
type ReportRow struct {
	Group string  `json:"group"`
	Value float64 `json:"value"`
}

// FillTime gives a time-grouped report a row for every period in [from, to), zero where nothing
// happened, so a line chart has no gaps. Groups are "YYYY-MM-DD" period starts.
func FillTime(p Period, from, to time.Time, rows []ReportRow) []ReportRow {
	have := map[string]float64{}
	for _, r := range rows {
		have[r.Group] = r.Value
	}
	out := []ReportRow{}
	for _, b := range p.Buckets(from, to) {
		k := b.Format(time.DateOnly)
		out = append(out, ReportRow{Group: k, Value: have[k]})
	}
	return out
}

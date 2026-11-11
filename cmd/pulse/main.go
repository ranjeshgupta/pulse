// Command pulse runs one role of the platform, or all of them:
//
//	pulse migrate   apply Postgres and ClickHouse migrations, create Kafka topics
//	pulse api       HTTP: event ingestion + admin API
//	pulse profile   events.raw -> identity + profile -> events.enriched
//	pulse chsink    events.enriched, segment.membership -> ClickHouse
//	pulse segments  events.enriched -> segment membership (+ backfills and sweeps)
//	pulse journeys  segment.membership, events.enriched -> journey steps (+ timers)
//	pulse dispatch  messages.<channel> -> email, SMS, WhatsApp, RCS, push and app inbox providers
//	pulse webhooks  events -> customers' outbound webhooks (and Zapier / Make subscriptions)
//	pulse integrations  CRM pulls on a schedule, ad audience sync (segment.membership, events.enriched),
//	                    bounce mailboxes (IMAP)
//	pulse all       migrate, then every role in one process (local development)
//	pulse replay-dlq [--limit N] [--dry-run] <topic or channel>
//	                move records from <topic>.dlq back to <topic> (after fixing what failed them)
//
// In production each role is its own deployment and scales on its own Kafka consumer group.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // time zones (quiet hours, local-time broadcasts) without zoneinfo in the image

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/errgroup"

	"pulse/internal/api"
	"pulse/internal/auth"
	"pulse/internal/bounces"
	"pulse/internal/broadcast"
	"pulse/internal/channel"
	"pulse/internal/chsink"
	"pulse/internal/config"
	"pulse/internal/dispatch"
	"pulse/internal/event"
	"pulse/internal/eventschema"
	"pulse/internal/geoip"
	"pulse/internal/importer"
	"pulse/internal/integrations"
	"pulse/internal/journey"
	"pulse/internal/kafka"
	"pulse/internal/live"
	"pulse/internal/maintenance"
	"pulse/internal/onsite"
	"pulse/internal/profile"
	"pulse/internal/providers"
	"pulse/internal/reach"
	"pulse/internal/safehttp"
	"pulse/internal/segengine"
	"pulse/internal/segment"
	"pulse/internal/store/ch"
	"pulse/internal/store/pg"
	"pulse/internal/tracking"
	"pulse/internal/webhooks"
	"pulse/migrations"
	"pulse/web"
)

var roles = map[string]func(context.Context, *deps) error{
	"migrate":      runMigrate,
	"api":          runAPI,
	"profile":      runProfile,
	"chsink":       runCHSink,
	"segments":     runSegments,
	"journeys":     runJourneys,
	"dispatch":     runDispatch,
	"webhooks":     runWebhooks,
	"integrations": runIntegrations,
	"all":          runAll,
	"replay-dlq":   runReplayDLQ,
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if len(os.Args) < 2 || roles[os.Args[1]] == nil {
		fmt.Fprintln(os.Stderr, "usage: pulse migrate|api|profile|chsink|segments|journeys|dispatch|webhooks|integrations|all|replay-dlq")
		os.Exit(2)
	}
	role := os.Args[1]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		log.Error("bad settings", "role", role, "err", err)
		os.Exit(2)
	}
	d := &deps{cfg: cfg, base: log, log: log.With("role", role)}
	defer d.close()
	if err := roles[role](ctx, d); err != nil && !errors.Is(err, context.Canceled) {
		log.Error("exit", "role", role, "err", err)
		os.Exit(1)
	}
}

// deps opens shared clients lazily so each role only connects to what it uses.
type deps struct {
	cfg  config.Config
	base *slog.Logger
	log  *slog.Logger

	pgs  *pg.Store
	chs  *ch.Store
	rdb  *redis.Client
	prod *kafka.Producer
}

func (d *deps) pg(ctx context.Context) (*pg.Store, error) {
	if d.pgs == nil {
		s, err := pg.Open(ctx, d.cfg.PGDSN)
		if err != nil {
			return nil, err
		}
		d.pgs = s
	}
	return d.pgs, nil
}

func (d *deps) ch(ctx context.Context) (*ch.Store, error) {
	if d.chs == nil {
		s, err := ch.Open(ctx, d.cfg.CHAddr, d.cfg.CHDB, d.cfg.CHUser, d.cfg.CHPassword)
		if err != nil {
			return nil, err
		}
		d.chs = s
	}
	return d.chs, nil
}

func (d *deps) redis() *redis.Client {
	if d.rdb == nil {
		d.rdb = redis.NewClient(&redis.Options{Addr: d.cfg.RedisAddr})
	}
	return d.rdb
}

func (d *deps) producer() (*kafka.Producer, error) {
	if d.prod == nil {
		p, err := kafka.NewProducer(d.cfg.KafkaBrokers)
		if err != nil {
			return nil, err
		}
		d.prod = p
	}
	return d.prod, nil
}

// box opens stored secrets. Without PULSE_SECRET_KEY, SSO and provider credentials are unavailable.
func (d *deps) box() (*auth.Box, error) {
	if d.cfg.SecretKey == "" {
		d.log.Warn("PULSE_SECRET_KEY not set: SSO and provider credentials are disabled")
		return nil, nil
	}
	return auth.NewBox(d.cfg.SecretKey)
}

// integrations builds the integrations service (CRM pulls and pushes) with the SSRF-safe client.
func (d *deps) integrations(ctx context.Context, box *auth.Box) (*integrations.Service, error) {
	pgs, err := d.pg(ctx)
	if err != nil {
		return nil, err
	}
	prod, err := d.producer()
	if err != nil {
		return nil, err
	}
	var open func([]byte) (map[string]string, error)
	if box != nil {
		open = func(sealed []byte) (map[string]string, error) {
			b, err := box.Open(sealed)
			if err != nil {
				return nil, err
			}
			var m map[string]string
			return m, json.Unmarshal(b, &m)
		}
	}
	return &integrations.Service{Store: pgs, Open: open, HTTP: safehttp.Client(30*time.Second, d.cfg.AllowPrivateWebhooks),
		Pub: prod, Log: d.log, Now: time.Now}, nil
}

// smtpHost is the host part of host:port.
func smtpHost(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return addr
}

// tracking builds the email link codec (nil without a secret key: emails then go out untracked).
func (d *deps) tracking(box *auth.Box) *tracking.Codec {
	if box == nil {
		return nil
	}
	return tracking.New(box, d.cfg.SecretKey, d.cfg.PublicURL)
}

func (d *deps) consumer(group string, topics ...string) (*kafka.Consumer, error) {
	p, err := d.producer()
	if err != nil {
		return nil, err
	}
	return kafka.NewConsumer(d.cfg.KafkaBrokers, group, p, d.log, topics...)
}

func (d *deps) close() {
	if d.prod != nil {
		d.prod.Close()
	}
	if d.rdb != nil {
		_ = d.rdb.Close()
	}
	if d.chs != nil {
		_ = d.chs.Close()
	}
	if d.pgs != nil {
		d.pgs.Close()
	}
}

// pipelineTopics are the topics `pulse migrate` creates, each with a .dlq twin.
func pipelineTopics() []string {
	return append([]string{event.TopicRaw, event.TopicEnriched, event.TopicMembership}, dispatch.Topics()...)
}

func runMigrate(ctx context.Context, d *deps) error {
	pgs, err := d.pg(ctx)
	if err != nil {
		return err
	}
	// One process migrates at a time (Postgres and ClickHouse alike); the others wait, then skip.
	release, err := pgs.MigrationLock(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := pgs.Migrate(ctx, migrations.Postgres, "postgres"); err != nil {
		return err
	}
	chs, err := d.ch(ctx)
	if err != nil {
		return err
	}
	if err := chs.Migrate(ctx, migrations.ClickHouse, "clickhouse"); err != nil {
		return err
	}
	var kerr error
	for i := 0; i < 30; i++ { // the broker may still be starting
		if kerr = kafka.EnsureTopics(ctx, d.cfg.KafkaBrokers, d.cfg.Partitions, pipelineTopics()...); kerr == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if kerr != nil {
		return fmt.Errorf("create topics: %w", kerr)
	}
	d.log.Info("migrations applied")
	return nil
}

func runAPI(ctx context.Context, d *deps) error {
	pgs, err := d.pg(ctx)
	if err != nil {
		return err
	}
	prod, err := d.producer()
	if err != nil {
		return err
	}
	box, err := d.box()
	if err != nil {
		return err
	}
	var geo *geoip.Locator
	if d.cfg.GeoIPDB != "" {
		if geo, err = geoip.Open(d.cfg.GeoIPDB); err != nil {
			return fmt.Errorf("geoip database %s: %w", d.cfg.GeoIPDB, err)
		}
		defer geo.Close()
	}
	chs, err := d.ch(ctx)
	if err != nil {
		return err
	}
	server := api.New(api.Deps{
		Store: pgs, Members: segment.Membership{RDB: d.redis()}, Analytics: chs, Pub: prod, RDB: d.redis(), Box: box,
		AdminToken: d.cfg.AdminToken, PublicURL: d.cfg.PublicURL, AllowSignup: d.cfg.AllowSignup, Log: d.log,
		Geo: geo, PagesURL: d.cfg.PagesURL, AllowPrivateSSO: d.cfg.AllowPrivateSSO, TrustProxy: d.cfg.TrustProxy, AllowPrivateWebhooks: d.cfg.AllowPrivateWebhooks,
		Tracking: d.tracking(box), Deleter: &maintenance.Cleaner{Store: pgs, Analytics: chs, Log: d.log},
		Schemas: eventschema.NewCache(pgs, d.redis(), d.log),
	})
	srv := &http.Server{
		Addr:              d.cfg.HTTPAddr,
		Handler:           server.WithUI(web.Handler()).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	d.log.Info("listening", "addr", d.cfg.HTTPAddr)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return ctx.Err()
}

func runProfile(ctx context.Context, d *deps) error {
	pgs, err := d.pg(ctx)
	if err != nil {
		return err
	}
	prod, err := d.producer()
	if err != nil {
		return err
	}
	c, err := d.consumer("pulse-profile", event.TopicRaw)
	if err != nil {
		return err
	}
	defer c.Close()
	svc := profile.New(pgs, d.redis(), prod)
	svc.Schemas, svc.Log = eventschema.NewCache(pgs, d.redis(), d.log), d.log
	return c.RunPartitionBatches(ctx, svc.HandleBatch)
}

func runCHSink(ctx context.Context, d *deps) error {
	chs, err := d.ch(ctx)
	if err != nil {
		return err
	}
	c, err := d.consumer("pulse-chsink", event.TopicEnriched, event.TopicMembership)
	if err != nil {
		return err
	}
	defer c.Close()
	sink := chsink.New(chs, d.log)
	sink.Live = live.Feed{RDB: d.redis()}
	return c.RunBatch(ctx, sink.Handle)
}

func runSegments(ctx context.Context, d *deps) error {
	pgs, err := d.pg(ctx)
	if err != nil {
		return err
	}
	chs, err := d.ch(ctx)
	if err != nil {
		return err
	}
	prod, err := d.producer()
	if err != nil {
		return err
	}
	eng := segengine.New(pgs, chs, d.redis(), prod, d.log, d.cfg.SweepInterval)
	if err := eng.Refresh(ctx); err != nil {
		return err
	}
	c, err := d.consumer("pulse-segments", event.TopicEnriched)
	if err != nil {
		return err
	}
	defer c.Close()
	// Dependents of segments changed by backfills, sweeps and list edits.
	mc, err := d.consumer("pulse-segments-deps", event.TopicMembership)
	if err != nil {
		return err
	}
	defer mc.Close()
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return eng.RunMaintenance(ctx) })
	// Reachability bitmaps (package reach): rebuilt when missing and daily; events keep them current.
	rb := &reach.Rebuilder{Store: pgs, RDB: d.redis(), Log: d.log}
	g.Go(func() error { return rb.Run(ctx) })
	g.Go(func() error { return c.RunPartitionBatches(ctx, eng.HandleBatch) })
	g.Go(func() error { return mc.RunPartitionBatches(ctx, eng.HandleMembership) })
	return g.Wait()
}

func runJourneys(ctx context.Context, d *deps) error {
	pgs, err := d.pg(ctx)
	if err != nil {
		return err
	}
	prod, err := d.producer()
	if err != nil {
		return err
	}
	sender := journey.KafkaSender{Pub: prod}
	rdb := d.redis()
	box, err := d.box()
	if err != nil {
		return err
	}
	ints, err := d.integrations(ctx, box)
	if err != nil {
		return err
	}
	chs, err := d.ch(ctx)
	if err != nil {
		return err
	}
	eng := journey.NewEngine(pgs, &journey.Executor{
		Membership: segment.Membership{RDB: rdb}, Sender: sender, Events: sender, Contacts: pgs,
		Counters: func(tenant int64, contact uint32, asOf time.Time) segment.Counter {
			return segment.NewRedisCounter(rdb, tenant, contact, asOf)
		},
		HTTP:         safehttp.Client(10*time.Second, d.cfg.AllowPrivateWebhooks),
		Data:         pgs,
		Onsite:       onsite.Pushes{RDB: rdb},
		History:      chs,
		Deleter:      &maintenance.Cleaner{Store: pgs, Analytics: chs, Log: d.log},
		MX:           &journey.DNSChecker{},
		Stats:        journey.RedisStepStats{RDB: rdb},
		Integrations: ints,
	}, d.log)
	eng.Schemas = eventschema.NewCache(pgs, rdb, d.log) // personal properties stay out of messages
	if err := eng.Refresh(ctx); err != nil {
		return err
	}
	c, err := d.consumer("pulse-journeys", event.TopicMembership, event.TopicEnriched)
	if err != nil {
		return err
	}
	defer c.Close()
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return eng.RunTimers(ctx) })
	g.Go(func() error { return eng.WatchChanges(ctx, rdb) })
	g.Go(func() error { return runBroadcasts(ctx, pgs, d, prod) })
	g.Go(func() error { return runImports(ctx, pgs, d, prod) })
	g.Go(func() error { return runMaintenance(ctx, pgs, d) })
	g.Go(func() error { return runSplitWinners(ctx, pgs, d) })
	g.Go(func() error { return c.Run(ctx, eng.Handle) })
	return g.Wait()
}

// runSplitWinners decides journey A/B tests (split steps with a winner setting) that are due.
func runSplitWinners(ctx context.Context, pgs *pg.Store, d *deps) error {
	chs, err := d.ch(ctx)
	if err != nil {
		return err
	}
	p := &journey.WinnerPicker{Store: pgs, Stats: chs, Log: d.log, Now: time.Now}
	return p.Run(ctx, 5*time.Second)
}

// runBroadcasts sends due broadcast runs. It lives with the journey workers: both turn schedules
// into send requests.
func runBroadcasts(ctx context.Context, pgs *pg.Store, d *deps, pub kafka.Publisher) error {
	chs, err := d.ch(ctx)
	if err != nil {
		return err
	}
	r := &broadcast.Runner{Store: pgs, Members: segment.Membership{RDB: d.redis()}, Stats: chs, Pub: pub, Log: d.log, Now: time.Now,
		Snapshot: broadcast.ReachSnapshot(d.redis(), time.Now)}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		if err := r.RunDue(ctx); err != nil && ctx.Err() == nil {
			d.log.Error("broadcasts", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

func runDispatch(ctx context.Context, d *deps) error {
	pgs, err := d.pg(ctx)
	if err != nil {
		return err
	}
	prod, err := d.producer()
	if err != nil {
		return err
	}
	box, err := d.box()
	if err != nil {
		return err
	}
	disp := dispatch.New(pgs, d.redis(), prod, dispatch.Config{
		From:         d.cfg.SMTPFrom,
		EmailDefault: &providers.SMTP{Addr: d.cfg.SMTPAddr, Service: providers.DetectService(smtpHost(d.cfg.SMTPAddr))},
		Rates:        map[string]float64{channel.Email: d.cfg.EmailRate},
		Box:          box,
		Providers:    providers.Deps{Sandbox: pgs, AllowPrivate: d.cfg.AllowPrivateWebhooks},
		Tracking:     d.tracking(box),
		PushSubject:  pushSubject(d.cfg.PublicURL),
		Log:          d.log,
	})
	c, err := d.consumer("pulse-dispatch", dispatch.Topics()...)
	if err != nil {
		return err
	}
	defer c.Close()
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return c.Run(ctx, disp.Handle) })
	g.Go(func() error { return disp.RunDeferred(ctx) })
	return g.Wait()
}

// pushSubject is the contact web push services may use: the public site when it's https,
// otherwise a placeholder address (local development).
func pushSubject(publicURL string) string {
	if strings.HasPrefix(publicURL, "https://") {
		return publicURL
	}
	return "mailto:push@pulse.invalid"
}

// runMaintenance deletes data past each workspace's retention settings (see internal/maintenance).
func runMaintenance(ctx context.Context, pgs *pg.Store, d *deps) error {
	chs, err := d.ch(ctx)
	if err != nil {
		return err
	}
	c := &maintenance.Cleaner{Store: pgs, Analytics: chs, Log: d.log}
	go func() {
		tick := time.NewTicker(time.Hour)
		defer tick.Stop()
		for {
			if err := pgs.PruneJourneyEffects(ctx); err != nil && ctx.Err() == nil {
				d.log.Error("prune journey effects", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return c.Loop(ctx, d.cfg.MaintenanceInterval)
}

// runImports streams queued background imports through the pipeline.
func runImports(ctx context.Context, pgs *pg.Store, d *deps, pub kafka.Publisher) error {
	j := &importer.Jobs{Store: pgs, Pub: pub, Log: d.log}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if err := j.RunDue(ctx); err != nil && ctx.Err() == nil {
			d.log.Error("imports", "err", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// runWebhooks posts events to workspaces' outbound webhooks.
func runWebhooks(ctx context.Context, d *deps) error {
	pgs, err := d.pg(ctx)
	if err != nil {
		return err
	}
	box, err := d.box()
	if err != nil {
		return err
	}
	open := func(sealed []byte) (string, error) {
		if box == nil {
			return "", errors.New("no secret key")
		}
		b, err := box.Open(sealed)
		return string(b), err
	}
	wk := &webhooks.Worker{Store: webhooks.Opened(pgs, open), RDB: d.redis(),
		Client: safehttp.Client(10*time.Second, d.cfg.AllowPrivateWebhooks), Log: d.log}
	c, err := d.consumer("pulse-webhooks", webhooks.Topics()...)
	if err != nil {
		return err
	}
	defer c.Close()
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return c.RunPartitionBatches(ctx, wk.HandleBatch) })
	g.Go(func() error { return wk.RunRetries(ctx) })
	return g.Wait()
}

// runIntegrations pulls CRM contacts on schedule and keeps ad audiences in step with segments.
func runIntegrations(ctx context.Context, d *deps) error {
	pgs, err := d.pg(ctx)
	if err != nil {
		return err
	}
	box, err := d.box()
	if err != nil {
		return err
	}
	svc, err := d.integrations(ctx, box)
	if err != nil {
		return err
	}
	syncer := &integrations.Syncer{Store: pgs, Members: integrations.RedisMembers{RDB: d.redis()}, Service: svc, Log: d.log}
	c, err := d.consumer("pulse-integrations", integrations.AudienceTopics()...)
	if err != nil {
		return err
	}
	defer c.Close()
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return c.RunPartitionBatches(ctx, syncer.HandleBatch) })
	g.Go(func() error { return syncer.Run(ctx, d.cfg.AudienceReconcile) })
	g.Go(func() error { return svc.RunPulls(ctx, 10*time.Second) })
	if box != nil { // bounce mailboxes keep their passwords sealed: without the key there are none to read
		prod, err := d.producer()
		if err != nil {
			return err
		}
		mail := &bounces.Poller{Store: pgs, Open: bounces.IMAPOpener(d.cfg.AllowPrivateWebhooks), Pub: bounces.KafkaPublisher{Pub: prod},
			Unseal: func(b []byte) (string, error) { p, err := box.Open(b); return string(p), err }, Log: d.log,
			Refs: d.tracking(box)}
		g.Go(func() error { return mail.Run(ctx, 10*time.Second) })
	}
	return g.Wait()
}

func runAll(ctx context.Context, d *deps) error {
	if err := runMigrate(ctx, d); err != nil {
		return err
	}
	workers := []struct {
		name string
		run  func(context.Context, *deps) error
	}{
		{"api", runAPI}, {"profile", runProfile}, {"chsink", runCHSink},
		{"segments", runSegments}, {"journeys", runJourneys}, {"dispatch", runDispatch}, {"webhooks", runWebhooks},
		{"integrations", runIntegrations},
	}
	g, ctx := errgroup.WithContext(ctx)
	for _, w := range workers {
		rd := &deps{cfg: d.cfg, base: d.base, log: d.base.With("role", w.name)}
		g.Go(func() error {
			defer rd.close()
			return w.run(ctx, rd)
		})
	}
	return g.Wait()
}

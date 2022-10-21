package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Probe struct {
	Name             *string
	Host             *string
	Interval         string
	Iterations       int
	Timeout          string
	Workers          int
	intervalDuration time.Duration
	timeoutDuration  time.Duration
	logger           zerolog.Logger
}

type probeFlag []Probe

var (
	probes       probeFlag
	port         int
	resolver     = net.Resolver{}
	defaultProbe = Probe{
		Iterations: 5,
		Interval:   "1s",
		Timeout:    "5s",
		Workers:    2,
	}

	lookupDurationHistogram = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "dns_lookup_duration_seconds",
			Help:    "Lookup duration distribution",
			Buckets: prometheus.ExponentialBuckets(0.0001, 10, 6),
		},
		[]string{"name", "host", "timeout"},
	)

	lookupFailedCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dns_lookup_failed_total",
			Help: "Failed lookups",
		},
		[]string{"name", "host", "timeout"},
	)

	collectors = []prometheus.Collector{
		lookupDurationHistogram,
		lookupFailedCounter,
	}
)

func init() {
	for _, c := range collectors {
		prometheus.MustRegister(c)
	}
}

func (i *probeFlag) String() string {
	b, _ := json.MarshalIndent(i, "", "  ")
	return string(b)
}

func (i *probeFlag) Set(value string) error {
	probe := defaultProbe

	err := json.Unmarshal([]byte(value), &probe)
	if err != nil {
		return err
	}

	if probe.Host == nil {
		return errors.New("invalid host")
	}

	if probe.Name == nil {
		return errors.New("invalid name")
	}

	interval, err := time.ParseDuration(probe.Interval)
	if err != nil {
		return err
	}

	timeout, err := time.ParseDuration(probe.Timeout)
	if err != nil {
		return err
	}

	probe.intervalDuration = interval
	probe.timeoutDuration = timeout

	probe.logger = log.With().
		Str("probe", *probe.Name).
		Str("timeout", probe.Timeout).
		Str("interval", probe.Interval).
		Int("workers", probe.Workers).
		Int("iterations", probe.Iterations).
		Str("host", *probe.Host).Logger()

	*i = append(*i, probe)

	return nil
}

func main() {
	flag.Var(&probes, "probe", "")
	flag.IntVar(&port, "port", 8080, "")
	flag.Parse()

	fmt.Println(probes.String())

	for _, probe := range probes {
		go func(probe Probe) {
			for {
				wg := sync.WaitGroup{}
				for i := 0; i < probe.Workers; i++ {
					wg.Add(1)
					go func(id int) {
						lookup(id, probe)
						wg.Done()
					}(i)
				}
				wg.Wait()
				time.Sleep(probe.intervalDuration)
			}
		}(probe)
	}

	http.Handle("/metrics", promhttp.Handler())
	panic(http.ListenAndServe(fmt.Sprintf(":%d", port), nil))
}

func lookup(id int, probe Probe) {
	failCount := 0

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for i := 0; i < probe.Iterations; i++ {
		start := time.Now()

		ctx, cancel := context.WithTimeout(ctx, probe.timeoutDuration)
		defer cancel()

		_, err := resolver.LookupIPAddr(ctx, *probe.Host)
		if err != nil {
			failCount++

			probe.logger.Error().
				Err(err).
				Int("worker", id).
				Msg("lookup failed")

			lookupFailedCounter.
				WithLabelValues(*probe.Name, *probe.Host, probe.Timeout).
				Inc()
		}

		duration := time.Since(start)
		lookupDurationHistogram.
			WithLabelValues(*probe.Name, *probe.Host, probe.Timeout).
			Observe(duration.Seconds())
	}

	probe.logger.Info().
		Int("worker", id).
		Int("failed", failCount).
		Msg("iteration completed")
}

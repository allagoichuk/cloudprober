// Copyright 2017 The Cloudprober Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metrics

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind represents EventMetrics type. There are currently only two kinds
// of EventMetrics supported: CUMULATIVE and GAUGE
type Kind int

const (
	// CUMULATIVE metrics accumulate with time and are usually used to
	// represent counters, e.g. number of requests.
	CUMULATIVE = iota
	// GAUGE metrics are used to represent values at a certain point of
	// time, e.g. pending queries.
	GAUGE
)

// EventMetrics respresents metrics associated with a particular time event.
type EventMetrics struct {
	mu        sync.RWMutex
	Timestamp time.Time
	Kind      Kind

	// Keys are metrics names
	metrics     map[string]Value
	metricsKeys []string

	// Labels are the labels associated with a particular set of metrics,
	// e.g. ptype=ping, dst=google.com, etc.
	labels     map[string]string
	labelsKeys []string

	LatencyUnit time.Duration
}

// NewEventMetrics return a new EventMetrics object with internals maps initialized.
func NewEventMetrics(ts time.Time) *EventMetrics {
	return &EventMetrics{
		Timestamp: ts,
		metrics:   make(map[string]Value),
		labels:    make(map[string]string),
	}
}

// AddMetric adds a metric (name & value) into the receiver EventMetric. If a
// metric with the same name exists already, new metric is ignored. AddMetric
// returns the receiver EventMetrics to allow for the chaining of these calls,
// for example:
//
//	em := metrics.NewEventMetrics(time.Now()).
//		AddMetric("sent", &prr.sent).
//		AddMetric("rcvd", &prr.rcvd).
//		AddMetric("rtt", &prr.rtt)
func (em *EventMetrics) AddMetric(name string, val Value) *EventMetrics {
	em.mu.Lock()
	defer em.mu.Unlock()

	if _, ok := em.metrics[name]; ok {
		// TODO(manugarg): We should probably log such cases. We'll have to
		// plumb logger for that.
		return em
	}
	em.metrics[name] = val
	em.metricsKeys = append(em.metricsKeys, name)
	return em
}

// Metric returns an EventMetrics metric value by name. Metric will return nil
// for a non-existent metric.
func (em *EventMetrics) Metric(name string) Value {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return em.metrics[name]
}

// MetricsKeys returns the list of all metric keys.
func (em *EventMetrics) MetricsKeys() []string {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return append([]string{}, em.metricsKeys...)
}

// AddLabel adds a label (name & value) into the receiver EventMetrics. If a
// label with the same name exists already, new label is ignored. AddLabel
// returns the receiver EventMetrics to allow for the chaining of these calls,
// for example:
//
//	em := metrics.NewEventMetrics(time.Now()).
//		AddMetric("sent", &prr.sent).
//		AddLabel("ptype", "http").
//		AddLabel("dst", target)
func (em *EventMetrics) AddLabel(name string, val string) *EventMetrics {
	em.mu.Lock()
	defer em.mu.Unlock()
	if _, ok := em.labels[name]; ok {
		// TODO(manugarg): We should probably log such cases. We'll have to
		// plumb logger for that.
		return em
	}
	em.labels[name] = val
	em.labelsKeys = append(em.labelsKeys, name)
	return em
}

// Label returns an EventMetrics label value by name. Label will return a
// zero-string ("") for a non-existent label.
func (em *EventMetrics) Label(name string) string {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return em.labels[name]
}

// LabelsKeys returns the list of all label keys.
func (em *EventMetrics) LabelsKeys() []string {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return append([]string{}, em.labelsKeys...)
}

// Clone clones the underlying fields. This is useful for creating copies of the EventMetrics objects.
func (em *EventMetrics) Clone() *EventMetrics {
	em.mu.RLock()
	defer em.mu.RUnlock()
	newEM := &EventMetrics{
		Timestamp: em.Timestamp,
		Kind:      em.Kind,
		metrics:   make(map[string]Value),
		labels:    make(map[string]string),
	}
	for _, lk := range em.labelsKeys {
		newEM.labels[lk] = em.labels[lk]
		newEM.labelsKeys = append(newEM.labelsKeys, lk)
	}
	for _, mk := range em.metricsKeys {
		newEM.metrics[mk] = em.metrics[mk].Clone()
		newEM.metricsKeys = append(newEM.metricsKeys, mk)
	}
	return newEM
}

// Update updates the receiver EventMetrics with the incoming one.
func (em *EventMetrics) Update(in *EventMetrics) error {
	if em.Kind != in.Kind {
		return fmt.Errorf("EventMetrics of different kind cannot be merged. Receiver's kind: %d, incoming: %d", em.Kind, in.Kind)
	}
	switch em.Kind {
	case GAUGE:
		for name, newVal := range in.metrics {
			_, ok := em.metrics[name]
			if !ok {
				return fmt.Errorf("receiver EventMetrics doesn't have %s metric", name)
			}
			em.metrics[name] = newVal.Clone()
		}
		return nil
	case CUMULATIVE:
		for name, newVal := range in.metrics {
			val, ok := em.metrics[name]
			if !ok {
				return fmt.Errorf("receiver EventMetrics doesn't have %s metric", name)
			}
			val.Add(newVal)
		}
		return nil
	default:
		return errors.New("Unknown metrics kind")
	}
}

// SubtractLast subtracts the provided (last) EventMetrics from the receiver
// EventMetrics and return the result as a GAUGE EventMetrics.
func (em *EventMetrics) SubtractLast(lastEM *EventMetrics) (*EventMetrics, error) {
	if em.Kind != CUMULATIVE || lastEM.Kind != CUMULATIVE {
		return nil, fmt.Errorf("incorrect eventmetrics kind (current: %v, last: %v), SubtractLast works only for CUMULATIVE metrics", em.Kind, lastEM.Kind)
	}

	gaugeEM := em.Clone()
	gaugeEM.Kind = GAUGE

	for name, lastVal := range lastEM.metrics {
		val, ok := gaugeEM.metrics[name]
		if !ok {
			return nil, fmt.Errorf("receiver EventMetrics doesn't have %s metric", name)
		}
		wasReset, err := val.SubtractCounter(lastVal)
		if err != nil {
			return nil, err
		}

		// If any metric is reset, consider it a full reset of EventMetrics.
		// TODO(manugarg): See if we can track this event somehow.
		if wasReset {
			gaugeEM := em.Clone()
			gaugeEM.Kind = GAUGE
			return gaugeEM, nil
		}
	}

	return gaugeEM, nil
}

// String returns the string representation of the EventMetrics.
// Note that this is compatible with what vmwatcher understands.
// Example output string:
// 1519084040 labels=ptype=http sent=62 rcvd=52 resp-code=map:code,200:44,204:8
func (em *EventMetrics) String() string {
	em.mu.RLock()
	defer em.mu.RUnlock()

	var b strings.Builder
	b.Grow(128)

	b.WriteString(strconv.FormatInt(em.Timestamp.Unix(), 10))
	// Labels section: labels=ptype=http,probe=homepage
	b.WriteString(" labels=")
	for i, key := range em.labelsKeys {
		if i != 0 {
			b.WriteByte(',')
		}
		b.WriteString(key)
		b.WriteByte('=')
		b.WriteString(em.labels[key])
	}
	// Values section: " sent=62 rcvd=52 resp-code=map:code,200:44,204:8"
	for _, name := range em.metricsKeys {
		b.WriteByte(' ')
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(em.metrics[name].String())
	}
	return b.String()
}

// Key returns a string key that uniquely identifies an eventmetrics.
func (em *EventMetrics) Key() string {
	em.mu.RLock()
	defer em.mu.RUnlock()

	var keys []string
	keys = append(keys, em.metricsKeys...)
	for _, k := range em.LabelsKeys() {
		keys = append(keys, k+"="+em.labels[k])
	}
	return strings.Join(keys, ",")
}

// LatencyUnitToString returns the string representation of the latency unit.
func LatencyUnitToString(latencyUnit time.Duration) string {
	if latencyUnit == 0 || latencyUnit == time.Microsecond {
		return "us"
	}
	return latencyUnit.String()[1:]
}

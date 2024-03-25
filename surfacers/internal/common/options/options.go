// Copyright 2021 The Cloudprober Authors.
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

// Package options defines data structure for common surfacer options.
package options

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"

	"github.com/cloudprober/cloudprober/config/runconfig"
	"github.com/cloudprober/cloudprober/logger"
	"github.com/cloudprober/cloudprober/metrics"
	surfacerpb "github.com/cloudprober/cloudprober/surfacers/proto"
)

type labelFilter struct {
	key   string
	value string
}

var defaultLatencyMetricRe = regexp.MustCompile("^(.*_|)latency$")

func (lf *labelFilter) matchEventMetrics(em *metrics.EventMetrics) bool {
	if lf.key != "" {
		for _, lKey := range em.LabelsKeys() {
			if lf.key != lKey {
				continue
			}
			if lf.value == "" {
				return true
			}
			return lf.value == em.Label(lKey)
		}
	}
	return false
}

func parseMetricsFilter(configs []*surfacerpb.LabelFilter) ([]*labelFilter, error) {
	var filters []*labelFilter

	for _, c := range configs {
		lf := &labelFilter{
			key:   c.GetKey(),
			value: c.GetValue(),
		}

		if lf.value != "" && lf.key == "" {
			return nil, fmt.Errorf("key is required to match against val (%s)", c.GetValue())
		}

		filters = append(filters, lf)
	}

	return filters, nil
}

// Options encapsulates surfacer options common to all surfacers.
type Options struct {
	MetricsBufferSize int
	Config            *surfacerpb.SurfacerDef
	Logger            *logger.Logger
	HTTPServeMux      *http.ServeMux

	// Metrics filtering
	allowLabelFilters  []*labelFilter
	ignoreLabelFilters []*labelFilter
	allowMetricName    *regexp.Regexp
	ignoreMetricName   *regexp.Regexp

	// latencyMetricRe is a regular expression to match latency metrics.
	latencyMetricRe *regexp.Regexp

	AddFailureMetric bool
}

// AllowEventMetrics returns whether a certain EventMetrics should be allowed
// or not.
// TODO(manugarg): Explore if we can either log or increment some metric when
// we ignore an EventMetrics.
func (opts *Options) AllowEventMetrics(em *metrics.EventMetrics) bool {
	if opts == nil {
		return true
	}

	// If we match any ignore filter, return false immediately.
	for _, ignoreF := range opts.ignoreLabelFilters {
		if ignoreF.matchEventMetrics(em) {
			return false
		}
	}

	// If no allow filters are given, allow everything.
	if len(opts.allowLabelFilters) == 0 {
		return true
	}

	// If allow filters are given, allow only if match them.
	for _, allowF := range opts.allowLabelFilters {
		if allowF.matchEventMetrics(em) {
			return true
		}
	}
	return false
}

// AllowMetric returns whether a certain Metric should be allowed or not.
func (opts *Options) AllowMetric(metricName string) bool {
	if opts == nil {
		return true
	}

	if opts.ignoreMetricName != nil && opts.ignoreMetricName.MatchString(metricName) {
		return false
	}

	if opts.allowMetricName == nil {
		return true
	}

	return opts.allowMetricName.MatchString(metricName)
}

func (opts *Options) IsLatencyMetric(metricName string) bool {
	if opts == nil {
		return defaultLatencyMetricRe.MatchString(metricName)
	}
	return opts.latencyMetricRe.MatchString(metricName)
}

// buildOptions builds surfacer options using config.
func buildOptions(sdef *surfacerpb.SurfacerDef, ignoreInit bool, l *logger.Logger) (*Options, error) {
	opts := &Options{
		Config:            sdef,
		Logger:            l,
		HTTPServeMux:      runconfig.DefaultHTTPServeMux(),
		MetricsBufferSize: int(sdef.GetMetricsBufferSize()),
	}

	serveMux := runconfig.DefaultHTTPServeMux()
	if serveMux == nil && !ignoreInit {
		return nil, errors.New("default ServeMux is not configured, called before cloudprober initialization")
	}
	opts.HTTPServeMux = serveMux

	var err error
	opts.allowLabelFilters, err = parseMetricsFilter(sdef.GetAllowMetricsWithLabel())
	if err != nil {
		return nil, err
	}

	opts.ignoreLabelFilters, err = parseMetricsFilter(sdef.GetIgnoreMetricsWithLabel())
	if err != nil {
		return nil, err
	}

	if sdef.GetAllowMetricsWithName() != "" {
		opts.allowMetricName, err = regexp.Compile(sdef.GetAllowMetricsWithName())
		if err != nil {
			return nil, err
		}
	}

	if sdef.GetIgnoreMetricsWithName() != "" {
		opts.ignoreMetricName, err = regexp.Compile(sdef.GetIgnoreMetricsWithName())
		if err != nil {
			return nil, err
		}
	}

	opts.AddFailureMetric = opts.Config.GetAddFailureMetric()
	defaultDisableFailureMetric := map[surfacerpb.Type]bool{
		surfacerpb.Type_FILE:   true,
		surfacerpb.Type_PUBSUB: true,
	}
	if opts.Config.AddFailureMetric == nil && !defaultDisableFailureMetric[opts.Config.GetType()] {
		opts.AddFailureMetric = true
	}

	re, err := regexp.Compile(opts.Config.GetLatencyMetricPattern())
	if err != nil {
		return nil, fmt.Errorf("invalid latency_metric_pattern: %s, err: %v", opts.Config.GetLatencyMetricPattern(), err)
	}
	opts.latencyMetricRe = re

	return opts, nil
}

// BuildOptionsFromConfig builds surfacer options using config.
func BuildOptionsFromConfig(sdef *surfacerpb.SurfacerDef, l *logger.Logger) (*Options, error) {
	return buildOptions(sdef, false, l)
}

// BuildOptionsForTest builds surfacer options using config for tests.
func BuildOptionsForTest(sdef *surfacerpb.SurfacerDef) *Options {
	ignoreInit := true

	opts, err := buildOptions(sdef, ignoreInit, nil)
	if err != nil {
		panic("Error building surfacer options for tests: " + err.Error())
	}

	return opts
}

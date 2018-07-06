/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package provider

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/kubernetes-incubator/custom-metrics-apiserver/pkg/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakedyn "k8s.io/client-go/dynamic/fake"

	config "github.com/kairosinc/custom-metrics-prometheus-adapter/cmd/config-gen/utils"
	prom "github.com/kairosinc/custom-metrics-prometheus-adapter/pkg/client"
	pmodel "github.com/prometheus/common/model"
)

const fakeProviderUpdateInterval = 2 * time.Second

// fakePromClient is a fake instance of prom.Client
type fakePromClient struct {
	// acceptibleInterval is the interval in which to return queries
	acceptibleInterval pmodel.Interval
	// errQueries are queries that result in an error (whether from Query or Series)
	errQueries map[prom.Selector]error
	// series are non-error responses to partial Series calls
	series map[prom.Selector][]prom.Series
	// queryResults are non-error responses to Query
	queryResults map[prom.Selector]prom.QueryResult
}

func (c *fakePromClient) Series(_ context.Context, interval pmodel.Interval, selectors ...prom.Selector) ([]prom.Series, error) {
	if (interval.Start != 0 && interval.Start < c.acceptibleInterval.Start) || (interval.End != 0 && interval.End > c.acceptibleInterval.End) {
		return nil, fmt.Errorf("interval [%v, %v] for query is outside range [%v, %v]", interval.Start, interval.End, c.acceptibleInterval.Start, c.acceptibleInterval.End)
	}
	res := []prom.Series{}
	for _, sel := range selectors {
		if err, found := c.errQueries[sel]; found {
			return nil, err
		}
		if series, found := c.series[sel]; found {
			res = append(res, series...)
		}
	}

	return res, nil
}

func (c *fakePromClient) Query(_ context.Context, t pmodel.Time, query prom.Selector) (prom.QueryResult, error) {
	if t < c.acceptibleInterval.Start || t > c.acceptibleInterval.End {
		return prom.QueryResult{}, fmt.Errorf("time %v for query is outside range [%v, %v]", t, c.acceptibleInterval.Start, c.acceptibleInterval.End)
	}

	if err, found := c.errQueries[query]; found {
		return prom.QueryResult{}, err
	}

	if res, found := c.queryResults[query]; found {
		return res, nil
	}

	return prom.QueryResult{
		Type:   pmodel.ValVector,
		Vector: &pmodel.Vector{},
	}, nil
}
func (c *fakePromClient) QueryRange(_ context.Context, r prom.Range, query prom.Selector) (prom.QueryResult, error) {
	return prom.QueryResult{}, nil
}

func setupPrometheusProvider(t *testing.T) (provider.CustomMetricsProvider, *fakePromClient) {
	fakeProm := &fakePromClient{}
	fakeKubeClient := &fakedyn.FakeDynamicClient{}

	cfg := config.DefaultConfig(1*time.Minute, "")
	namers, err := NamersFromConfig(cfg, restMapper())
	require.NoError(t, err)

	prov, _ := NewPrometheusProvider(restMapper(), fakeKubeClient, fakeProm, namers, fakeProviderUpdateInterval)

	containerSel := prom.MatchSeries("", prom.NameMatches("^container_.*"), prom.LabelNeq("container_name", "POD"), prom.LabelNeq("namespace", ""), prom.LabelNeq("pod_name", ""))
	namespacedSel := prom.MatchSeries("", prom.LabelNeq("namespace", ""), prom.NameNotMatches("^container_.*"))
	fakeProm.series = map[prom.Selector][]prom.Series{
		containerSel: {
			{
				Name:   "container_some_usage",
				Labels: pmodel.LabelSet{"pod_name": "somepod", "namespace": "somens", "container_name": "somecont"},
			},
		},
		namespacedSel: {
			{
				Name:   "ingress_hits_total",
				Labels: pmodel.LabelSet{"ingress": "someingress", "service": "somesvc", "pod": "backend1", "namespace": "somens"},
			},
			{
				Name:   "ingress_hits_total",
				Labels: pmodel.LabelSet{"ingress": "someingress", "service": "somesvc", "pod": "backend2", "namespace": "somens"},
			},
			{
				Name:   "service_proxy_packets",
				Labels: pmodel.LabelSet{"service": "somesvc", "namespace": "somens"},
			},
			{
				Name:   "work_queue_wait_seconds_total",
				Labels: pmodel.LabelSet{"deployment": "somedep", "namespace": "somens"},
			},
		},
	}

	return prov, fakeProm
}

func TestListAllMetrics(t *testing.T) {
	// setup
	prov, fakeProm := setupPrometheusProvider(t)

	// assume we have no updates
	require.Len(t, prov.ListAllMetrics(), 0, "assume: should have no metrics updates at the start")

	// set the acceptible interval (now until the next update, with a bit of wiggle room)
	startTime := pmodel.Now().Add(-1*fakeProviderUpdateInterval - fakeProviderUpdateInterval/10)
	fakeProm.acceptibleInterval = pmodel.Interval{Start: startTime, End: 0}

	// update the metrics (without actually calling RunUntil, so we can avoid timing issues)
	lister := prov.(*prometheusProvider).SeriesRegistry.(*cachingMetricsLister)
	require.NoError(t, lister.updateMetrics())

	// list/sort the metrics
	actualMetrics := prov.ListAllMetrics()
	sort.Sort(metricInfoSorter(actualMetrics))

	expectedMetrics := []provider.CustomMetricInfo{
		{schema.GroupResource{Resource: "services"}, true, "ingress_hits"},
		{schema.GroupResource{Group: "extensions", Resource: "ingresses"}, true, "ingress_hits"},
		{schema.GroupResource{Resource: "pods"}, true, "ingress_hits"},
		{schema.GroupResource{Resource: "namespaces"}, false, "ingress_hits"},
		{schema.GroupResource{Resource: "services"}, true, "service_proxy_packets"},
		{schema.GroupResource{Resource: "namespaces"}, false, "service_proxy_packets"},
		{schema.GroupResource{Group: "extensions", Resource: "deployments"}, true, "work_queue_wait"},
		{schema.GroupResource{Resource: "namespaces"}, false, "work_queue_wait"},
		{schema.GroupResource{Resource: "namespaces"}, false, "some_usage"},
		{schema.GroupResource{Resource: "pods"}, true, "some_usage"},
	}
	sort.Sort(metricInfoSorter(expectedMetrics))

	// assert that we got what we expected
	assert.Equal(t, expectedMetrics, actualMetrics)
}

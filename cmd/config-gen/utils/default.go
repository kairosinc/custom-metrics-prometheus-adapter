package utils

import (
	"fmt"
	"time"

	prom "github.com/kairosinc/custom-metrics-prometheus-adapter/pkg/client"
	. "github.com/kairosinc/custom-metrics-prometheus-adapter/pkg/config"
	pmodel "github.com/prometheus/common/model"
)

// DefaultConfig returns a configuration equivalent to the former
// pre-advanced-config settings.  This means that "normal" series labels
// will be of the form `<prefix><<.Resource>>`, cadvisor series will be
// of the form `container_`, and have the label `pod_name`.  Any series ending
// in total will be treated as a rate metric.
func DefaultConfig(rateInterval time.Duration, labelPrefix string) *MetricsDiscoveryConfig {
	return &MetricsDiscoveryConfig{
		Rules: []DiscoveryRule{
			// container seconds rate metrics
			{
				SeriesQuery: string(prom.MatchSeries("", prom.NameMatches("^container_.*"), prom.LabelNeq("container_name", "POD"), prom.LabelNeq("namespace", ""), prom.LabelNeq("pod_name", ""))),
				Resources: ResourceMapping{
					Overrides: map[string]GroupResource{
						"namespace": {Resource: "namespace"},
						"pod_name":  {Resource: "pod"},
					},
				},
				Name:         NameMapping{Matches: "^container_(.*)_seconds_total$"},
				MetricsQuery: fmt.Sprintf(`sum(rate(<<.Series>>{<<.LabelMatchers>>,container_name!="POD"}[%s])) by (<<.GroupBy>>)`, pmodel.Duration(rateInterval).String()),
			},

			// container rate metrics
			{
				SeriesQuery:   string(prom.MatchSeries("", prom.NameMatches("^container_.*"), prom.LabelNeq("container_name", "POD"), prom.LabelNeq("namespace", ""), prom.LabelNeq("pod_name", ""))),
				SeriesFilters: []RegexFilter{{IsNot: "^container_.*_seconds_total$"}},
				Resources: ResourceMapping{
					Overrides: map[string]GroupResource{
						"namespace": {Resource: "namespace"},
						"pod_name":  {Resource: "pod"},
					},
				},
				Name:         NameMapping{Matches: "^container_(.*)_total$"},
				MetricsQuery: fmt.Sprintf(`sum(rate(<<.Series>>{<<.LabelMatchers>>,container_name!="POD"}[%s])) by (<<.GroupBy>>)`, pmodel.Duration(rateInterval).String()),
			},

			// container non-cumulative metrics
			{
				SeriesQuery:   string(prom.MatchSeries("", prom.NameMatches("^container_.*"), prom.LabelNeq("container_name", "POD"), prom.LabelNeq("namespace", ""), prom.LabelNeq("pod_name", ""))),
				SeriesFilters: []RegexFilter{{IsNot: "^container_.*_total$"}},
				Resources: ResourceMapping{
					Overrides: map[string]GroupResource{
						"namespace": {Resource: "namespace"},
						"pod_name":  {Resource: "pod"},
					},
				},
				Name:         NameMapping{Matches: "^container_(.*)$"},
				MetricsQuery: `sum(<<.Series>>{<<.LabelMatchers>>,container_name!="POD"}) by (<<.GroupBy>>)`,
			},

			// normal non-cumulative metrics
			{
				SeriesQuery:   string(prom.MatchSeries("", prom.LabelNeq(fmt.Sprintf("%snamespace", labelPrefix), ""), prom.NameNotMatches("^container_.*"))),
				SeriesFilters: []RegexFilter{{IsNot: ".*_total$"}},
				Resources: ResourceMapping{
					Template: fmt.Sprintf("%s<<.Resource>>", labelPrefix),
				},
				MetricsQuery: "sum(<<.Series>>{<<.LabelMatchers>>}) by (<<.GroupBy>>)",
			},

			// normal rate metrics
			{
				SeriesQuery:   string(prom.MatchSeries("", prom.LabelNeq(fmt.Sprintf("%snamespace", labelPrefix), ""), prom.NameNotMatches("^container_.*"))),
				SeriesFilters: []RegexFilter{{IsNot: ".*_seconds_total"}},
				Name:          NameMapping{Matches: "^(.*)_total$"},
				Resources: ResourceMapping{
					Template: fmt.Sprintf("%s<<.Resource>>", labelPrefix),
				},
				MetricsQuery: fmt.Sprintf("sum(rate(<<.Series>>{<<.LabelMatchers>>}[%s])) by (<<.GroupBy>>)", pmodel.Duration(rateInterval).String()),
			},

			// seconds rate metrics
			{
				SeriesQuery: string(prom.MatchSeries("", prom.LabelNeq(fmt.Sprintf("%snamespace", labelPrefix), ""), prom.NameNotMatches("^container_.*"))),
				Name:        NameMapping{Matches: "^(.*)_seconds_total$"},
				Resources: ResourceMapping{
					Template: fmt.Sprintf("%s<<.Resource>>", labelPrefix),
				},
				MetricsQuery: fmt.Sprintf("sum(rate(<<.Series>>{<<.LabelMatchers>>}[%s])) by (<<.GroupBy>>)", pmodel.Duration(rateInterval).String()),
			},
		},
	}
}

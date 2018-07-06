package provider

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"text/template"

	"github.com/golang/glog"
	"github.com/kubernetes-incubator/custom-metrics-apiserver/pkg/provider"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime/schema"

	prom "github.com/kairosinc/custom-metrics-prometheus-adapter/pkg/client"
	"github.com/kairosinc/custom-metrics-prometheus-adapter/pkg/config"
	pmodel "github.com/prometheus/common/model"
)

var nsGroupResource = schema.GroupResource{Resource: "namespaces"}
var groupNameSanitizer = strings.NewReplacer(".", "_", "-", "_")

// MetricNamer knows how to convert Prometheus series names and label names to
// metrics API resources, and vice-versa.  MetricNamers should be safe to access
// concurrently.  Returned group-resources are "normalized" as per the
// MetricInfo#Normalized method.  Group-resources passed as arguments must
// themselves be normalized.
type MetricNamer interface {
	// Selector produces the appropriate Prometheus series selector to match all
	// series handlable by this namer.
	Selector() prom.Selector
	// FilterSeries checks to see which of the given series match any additional
	// constrains beyond the series query.  It's assumed that the series given
	// already matche the series query.
	FilterSeries(series []prom.Series) []prom.Series
	// ResourcesForSeries returns the group-resources associated with the given series,
	// as well as whether or not the given series has the "namespace" resource).
	ResourcesForSeries(series prom.Series) (res []schema.GroupResource, namespaced bool)
	// LabelForResource returns the appropriate label for the given resource.
	LabelForResource(resource schema.GroupResource) (pmodel.LabelName, error)
	// MetricNameForSeries returns the name (as presented in the API) for a given series.
	MetricNameForSeries(series prom.Series) (string, error)
	// QueryForSeries returns the query for a given series (not API metric name), with
	// the given namespace name (if relevant), resource, and resource names.
	QueryForSeries(series string, resource schema.GroupResource, namespace string, names ...string) (prom.Selector, error)
}

// labelGroupResExtractor extracts schema.GroupResources from series labels.
type labelGroupResExtractor struct {
	regex *regexp.Regexp

	resourceInd int
	groupInd    *int
	mapper      apimeta.RESTMapper
}

// newLabelGroupResExtractor creates a new labelGroupResExtractor for labels whose form
// matches the given template.  It does so by creating a regular expression from the template,
// so anything in the template which limits resource or group name length will cause issues.
func newLabelGroupResExtractor(labelTemplate *template.Template) (*labelGroupResExtractor, error) {
	labelRegexBuff := new(bytes.Buffer)
	if err := labelTemplate.Execute(labelRegexBuff, schema.GroupResource{"(?P<group>.+?)", "(?P<resource>.+?)"}); err != nil {
		return nil, fmt.Errorf("unable to convert label template to matcher: %v", err)
	}
	if labelRegexBuff.Len() == 0 {
		return nil, fmt.Errorf("unable to convert label template to matcher: empty template")
	}
	labelRegexRaw := "^" + labelRegexBuff.String() + "$"
	labelRegex, err := regexp.Compile(labelRegexRaw)
	if err != nil {
		return nil, fmt.Errorf("unable to convert label template to matcher: %v", err)
	}

	var groupInd *int
	var resInd *int

	for i, name := range labelRegex.SubexpNames() {
		switch name {
		case "group":
			ind := i // copy to avoid iteration variable reference
			groupInd = &ind
		case "resource":
			ind := i // copy to avoid iteration variable reference
			resInd = &ind
		}
	}

	if resInd == nil {
		return nil, fmt.Errorf("must include at least `{{.Resource}}` in the label template")
	}

	return &labelGroupResExtractor{
		regex:       labelRegex,
		resourceInd: *resInd,
		groupInd:    groupInd,
	}, nil
}

// GroupResourceForLabel extracts a schema.GroupResource from the given label, if possible.
// The second argument indicates whether or not a potential group-resource was found in this label.
func (e *labelGroupResExtractor) GroupResourceForLabel(lbl pmodel.LabelName) (schema.GroupResource, bool) {
	matchGroups := e.regex.FindStringSubmatch(string(lbl))
	if matchGroups != nil {
		group := ""
		if e.groupInd != nil {
			group = matchGroups[*e.groupInd]
		}

		return schema.GroupResource{
			Group:    group,
			Resource: matchGroups[e.resourceInd],
		}, true
	}

	return schema.GroupResource{}, false
}

func (r *metricNamer) Selector() prom.Selector {
	return r.seriesQuery
}

// reMatcher either positively or negatively matches a regex
type reMatcher struct {
	regex    *regexp.Regexp
	positive bool
}

func newReMatcher(cfg config.RegexFilter) (*reMatcher, error) {
	if cfg.Is != "" && cfg.IsNot != "" {
		return nil, fmt.Errorf("cannot have both an `is` (%q) and `isNot` (%q) expression in a single filter", cfg.Is, cfg.IsNot)
	}
	if cfg.Is == "" && cfg.IsNot == "" {
		return nil, fmt.Errorf("must have either an `is` or `isNot` expression in a filter")
	}

	var positive bool
	var regexRaw string
	if cfg.Is != "" {
		positive = true
		regexRaw = cfg.Is
	} else {
		positive = false
		regexRaw = cfg.IsNot
	}

	regex, err := regexp.Compile(regexRaw)
	if err != nil {
		return nil, fmt.Errorf("unable to compile series filter %q: %v", regexRaw, err)
	}

	return &reMatcher{
		regex:    regex,
		positive: positive,
	}, nil
}

func (m *reMatcher) Matches(val string) bool {
	return m.regex.MatchString(val) == m.positive
}

type metricNamer struct {
	seriesQuery          prom.Selector
	labelTemplate        *template.Template
	labelResExtractor    *labelGroupResExtractor
	metricsQueryTemplate *template.Template
	nameMatches          *regexp.Regexp
	nameAs               string
	seriesMatchers       []*reMatcher

	labelResourceMu sync.RWMutex
	labelToResource map[pmodel.LabelName]schema.GroupResource
	resourceToLabel map[schema.GroupResource]pmodel.LabelName
	mapper          apimeta.RESTMapper
}

// queryTemplateArgs are the arguments for the metrics query template.
type queryTemplateArgs struct {
	Series            string
	LabelMatchers     string
	LabelValuesByName map[string][]string
	GroupBy           string
	GroupBySlice      []string
}

func (n *metricNamer) FilterSeries(initialSeries []prom.Series) []prom.Series {
	if len(n.seriesMatchers) == 0 {
		return initialSeries
	}

	finalSeries := make([]prom.Series, 0, len(initialSeries))
SeriesLoop:
	for _, series := range initialSeries {
		for _, matcher := range n.seriesMatchers {
			if !matcher.Matches(series.Name) {
				continue SeriesLoop
			}
		}
		finalSeries = append(finalSeries, series)
	}

	return finalSeries
}

func (n *metricNamer) QueryForSeries(series string, resource schema.GroupResource, namespace string, names ...string) (prom.Selector, error) {
	var exprs []string
	valuesByName := map[string][]string{}

	if namespace != "" {
		namespaceLbl, err := n.LabelForResource(nsGroupResource)
		if err != nil {
			return "", err
		}
		exprs = append(exprs, prom.LabelEq(string(namespaceLbl), namespace))
		valuesByName[string(namespaceLbl)] = []string{namespace}
	}

	resourceLbl, err := n.LabelForResource(resource)
	if err != nil {
		return "", err
	}
	matcher := prom.LabelEq
	targetValue := names[0]
	if len(names) > 1 {
		matcher = prom.LabelMatches
		targetValue = strings.Join(names, "|")
	}
	exprs = append(exprs, matcher(string(resourceLbl), targetValue))
	valuesByName[string(resourceLbl)] = names

	args := queryTemplateArgs{
		Series:            series,
		LabelMatchers:     strings.Join(exprs, ","),
		LabelValuesByName: valuesByName,
		GroupBy:           string(resourceLbl),
		GroupBySlice:      []string{string(resourceLbl)},
	}
	queryBuff := new(bytes.Buffer)
	if err := n.metricsQueryTemplate.Execute(queryBuff, args); err != nil {
		return "", err
	}

	if queryBuff.Len() == 0 {
		return "", fmt.Errorf("empty query produced by metrics query template")
	}

	return prom.Selector(queryBuff.String()), nil
}

func (n *metricNamer) ResourcesForSeries(series prom.Series) ([]schema.GroupResource, bool) {
	// use an updates map to avoid having to drop the read lock to update the cache
	// until the end.  Since we'll probably have few updates after the first run,
	// this should mean that we rarely have to hold the write lock.
	var resources []schema.GroupResource
	updates := make(map[pmodel.LabelName]schema.GroupResource)
	namespaced := false

	// use an anon func to get the right defer behavior
	func() {
		n.labelResourceMu.RLock()
		defer n.labelResourceMu.RUnlock()

		for lbl := range series.Labels {
			var groupRes schema.GroupResource
			var ok bool

			// check if we have an override
			if groupRes, ok = n.labelToResource[lbl]; ok {
				resources = append(resources, groupRes)
			} else if groupRes, ok = updates[lbl]; ok {
				resources = append(resources, groupRes)
			} else if n.labelResExtractor != nil {
				// if not, check if it matches the form we expect, and if so,
				// convert to a group-resource.
				if groupRes, ok = n.labelResExtractor.GroupResourceForLabel(lbl); ok {
					info, _, err := provider.CustomMetricInfo{GroupResource: groupRes}.Normalized(n.mapper)
					if err != nil {
						glog.Errorf("unable to normalize group-resource %s from label %q, skipping: %v", groupRes.String(), lbl, err)
						continue
					}

					groupRes = info.GroupResource
					resources = append(resources, groupRes)
					updates[lbl] = groupRes
				}
			}

			if groupRes == nsGroupResource {
				namespaced = true
			}
		}
	}()

	// update the cache for next time.  This should only be called by discovery,
	// so we don't really have to worry about the grap between read and write locks
	// (plus, we don't care if someone else updates the cache first, since the results
	// are necessarily the same, so at most we've done extra work).
	if len(updates) > 0 {
		n.labelResourceMu.Lock()
		defer n.labelResourceMu.Unlock()

		for lbl, groupRes := range updates {
			n.labelToResource[lbl] = groupRes
		}
	}

	return resources, namespaced
}

func (n *metricNamer) LabelForResource(resource schema.GroupResource) (pmodel.LabelName, error) {
	n.labelResourceMu.RLock()
	// check if we have a cached copy or override
	lbl, ok := n.resourceToLabel[resource]
	n.labelResourceMu.RUnlock() // release before we call makeLabelForResource
	if ok {
		return lbl, nil
	}

	// NB: we don't actually care about the gap between releasing read lock
	// and acquiring the write lock -- if we do duplicate work sometimes, so be
	// it, as long as we're correct.

	// otherwise, use the template and save the result
	lbl, err := n.makeLabelForResource(resource)
	if err != nil {
		return "", fmt.Errorf("unable to convert resource %s into label: %v", resource.String(), err)
	}
	return lbl, nil
}

// makeLabelForResource constructs a label name for the given resource, and saves the result.
// It must *not* be called under an existing lock.
func (n *metricNamer) makeLabelForResource(resource schema.GroupResource) (pmodel.LabelName, error) {
	if n.labelTemplate == nil {
		return "", fmt.Errorf("no generic resource label form specified for this metric")
	}
	buff := new(bytes.Buffer)

	singularRes, err := n.mapper.ResourceSingularizer(resource.Resource)
	if err != nil {
		return "", fmt.Errorf("unable to singularize resource %s: %v", resource.String(), err)
	}
	convResource := schema.GroupResource{
		Group:    groupNameSanitizer.Replace(resource.Group),
		Resource: singularRes,
	}

	if err := n.labelTemplate.Execute(buff, convResource); err != nil {
		return "", err
	}
	if buff.Len() == 0 {
		return "", fmt.Errorf("empty label produced by label template")
	}
	lbl := pmodel.LabelName(buff.String())

	n.labelResourceMu.Lock()
	defer n.labelResourceMu.Unlock()

	n.resourceToLabel[resource] = lbl
	n.labelToResource[lbl] = resource
	return lbl, nil
}

func (n *metricNamer) MetricNameForSeries(series prom.Series) (string, error) {
	matches := n.nameMatches.FindStringSubmatchIndex(series.Name)
	if matches == nil {
		return "", fmt.Errorf("series name %q did not match expected pattern %q", series.Name, n.nameMatches.String())
	}
	outNameBytes := n.nameMatches.ExpandString(nil, n.nameAs, series.Name, matches)
	return string(outNameBytes), nil
}

// NamersFromConfig produces a MetricNamer for each rule in the given config.
func NamersFromConfig(cfg *config.MetricsDiscoveryConfig, mapper apimeta.RESTMapper) ([]MetricNamer, error) {
	namers := make([]MetricNamer, len(cfg.Rules))

	for i, rule := range cfg.Rules {
		var labelTemplate *template.Template
		var labelResExtractor *labelGroupResExtractor
		var err error
		if rule.Resources.Template != "" {
			labelTemplate, err = template.New("resource-label").Delims("<<", ">>").Parse(rule.Resources.Template)
			if err != nil {
				return nil, fmt.Errorf("unable to parse label template %q associated with series query %q: %v", rule.Resources.Template, rule.SeriesQuery, err)
			}

			labelResExtractor, err = newLabelGroupResExtractor(labelTemplate)
			if err != nil {
				return nil, fmt.Errorf("unable to generate label format from template %q associated with series query %q: %v", rule.Resources.Template, rule.SeriesQuery, err)
			}
		}

		metricsQueryTemplate, err := template.New("metrics-query").Delims("<<", ">>").Parse(rule.MetricsQuery)
		if err != nil {
			return nil, fmt.Errorf("unable to parse metrics query template %q associated with series query %q: %v", rule.MetricsQuery, rule.SeriesQuery, err)
		}

		seriesMatchers := make([]*reMatcher, len(rule.SeriesFilters))
		for i, filterRaw := range rule.SeriesFilters {
			matcher, err := newReMatcher(filterRaw)
			if err != nil {
				return nil, fmt.Errorf("unable to generate series name filter associated with series query %q: %v", rule.SeriesQuery, err)
			}
			seriesMatchers[i] = matcher
		}
		if rule.Name.Matches != "" {
			matcher, err := newReMatcher(config.RegexFilter{Is: rule.Name.Matches})
			if err != nil {
				return nil, fmt.Errorf("unable to generate series name filter from name rules associated with series query %q: %v", rule.SeriesQuery, err)
			}
			seriesMatchers = append(seriesMatchers, matcher)
		}

		var nameMatches *regexp.Regexp
		if rule.Name.Matches != "" {
			nameMatches, err = regexp.Compile(rule.Name.Matches)
			if err != nil {
				return nil, fmt.Errorf("unable to compile series name match expression %q associated with series query %q: %v", rule.Name.Matches, rule.SeriesQuery, err)
			}
		} else {
			// this will always succeed
			nameMatches = regexp.MustCompile(".*")
		}
		nameAs := rule.Name.As
		if nameAs == "" {
			// check if we have an obvious default
			subexpNames := nameMatches.SubexpNames()
			if len(subexpNames) == 1 {
				// no capture groups, use the whole thing
				nameAs = "$0"
			} else if len(subexpNames) == 2 {
				// one capture group, use that
				nameAs = "$1"
			} else {
				return nil, fmt.Errorf("must specify an 'as' value for name matcher %q associated with series query %q", rule.Name.Matches, rule.SeriesQuery)
			}
		}

		namer := &metricNamer{
			seriesQuery:          prom.Selector(rule.SeriesQuery),
			labelTemplate:        labelTemplate,
			labelResExtractor:    labelResExtractor,
			metricsQueryTemplate: metricsQueryTemplate,
			mapper:               mapper,
			nameMatches:          nameMatches,
			nameAs:               nameAs,
			seriesMatchers:       seriesMatchers,

			labelToResource: make(map[pmodel.LabelName]schema.GroupResource),
			resourceToLabel: make(map[schema.GroupResource]pmodel.LabelName),
		}

		// invert the structure for consistency with the template
		for lbl, groupRes := range rule.Resources.Overrides {
			infoRaw := provider.CustomMetricInfo{
				GroupResource: schema.GroupResource{
					Group:    groupRes.Group,
					Resource: groupRes.Resource,
				},
			}
			info, _, err := infoRaw.Normalized(mapper)
			if err != nil {
				return nil, fmt.Errorf("unable to normalize group-resource %v: %v", groupRes, err)
			}

			namer.labelToResource[pmodel.LabelName(lbl)] = info.GroupResource
			namer.resourceToLabel[info.GroupResource] = pmodel.LabelName(lbl)
		}

		namers[i] = namer
	}

	return namers, nil
}

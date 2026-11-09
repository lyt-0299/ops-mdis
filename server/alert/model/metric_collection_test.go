package model

import (
	"slices"
	"testing"
)

func TestMetricCollectionExporterArgs(t *testing.T) {
	cases := []struct {
		name, kind string
		config     *MetricCollection
		want       []string
		invalid    bool
	}{
		{"legacy source", KindMongoInstance, nil, nil, false},
		{"selected collections", KindMongoInstance, &MetricCollection{
			Collectors: []string{"collections", "indexes"}, Scope: []string{"app.events", "app.logs.daily"},
		}, []string{"--collector.collstats", "--collector.indexstats", "--mongodb.collstats-colls=app.events,app.logs.daily", "--mongodb.indexstats-colls=app.events,app.logs.daily"}, false},
		{"all collections", KindMongoInstance, &MetricCollection{
			Collectors: []string{"collections"}, Scope: []string{"*"},
		}, []string{"--collector.collstats", "--discovering-mode"}, false},
		{"all MySQL schemas", KindMySQLInstance, &MetricCollection{
			Collectors: []string{"tables", "statements"}, Scope: []string{"*"},
		}, []string{"--collect.info_schema.tables", "--collect.perf_schema.eventsstatements", "--collect.info_schema.tables.databases=*", "--collect.perf_schema.eventsstatements.limit=100"}, false},
		{"missing scope", KindMongoInstance, &MetricCollection{Collectors: []string{"indexes"}}, nil, true},
		{"unknown collector", KindMongoInstance, &MetricCollection{Collectors: []string{"--collect-all"}}, nil, true},
		{"unsafe SQL scope", KindMySQLInstance, &MetricCollection{Collectors: []string{"tables"}, Scope: []string{"app'"}}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args, err := c.config.ExporterArgs(c.kind)
			if (err != nil) != c.invalid || !slices.Equal(args, c.want) {
				t.Fatalf("ExporterArgs() = %v, %v; want %v, invalid=%t", args, err, c.want, c.invalid)
			}
		})
	}
}

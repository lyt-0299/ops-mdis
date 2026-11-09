package model

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type MetricCollection struct {
	Collectors []string `bson:"collectors,omitempty" json:"collectors,omitempty"`
	Scope      []string `bson:"scope,omitempty" json:"scope,omitempty"`
}

var mongoCollectionScope = regexp.MustCompile(`^[^.,\s*\x00-\x1f]+\.[^,\s*\x00-\x1f]+$`)
var mysqlDatabaseScope = regexp.MustCompile(`^[A-Za-z0-9_$-]+$`)

var mongoCollectorFlags = map[string]string{
	"databases":   "--collector.dbstats",
	"collections": "--collector.collstats",
	"indexes":     "--collector.indexstats",
	"operations":  "--collector.topmetrics",
}

var mysqlCollectorFlags = map[string]string{
	"tables":     "--collect.info_schema.tables",
	"tableIO":    "--collect.perf_schema.tableiowaits",
	"indexIO":    "--collect.perf_schema.indexiowaits",
	"tableLocks": "--collect.perf_schema.tablelocks",
	"statements": "--collect.perf_schema.eventsstatements",
	"innodb":     "--collect.info_schema.innodb_metrics",
}

// ExporterArgs validates the UI selection and returns only flags supported by the bundled exporters.
func (c *MetricCollection) ExporterArgs(kind string) ([]string, error) {
	if c == nil {
		return nil, nil
	}

	var flags map[string]string
	var scopePattern *regexp.Regexp
	switch kind {
	case KindMongoInstance:
		flags, scopePattern = mongoCollectorFlags, mongoCollectionScope
	case KindMySQLInstance:
		flags, scopePattern = mysqlCollectorFlags, mysqlDatabaseScope
	default:
		return nil, fmt.Errorf("metric collection is unavailable for this source type")
	}
	if len(c.Collectors) > len(flags) || len(c.Scope) > 50 {
		return nil, fmt.Errorf("too many collectors or scope entries (maximum 50)")
	}

	var args []string
	for _, name := range c.Collectors {
		flag, ok := flags[name]
		if !ok || slices.Contains(args, flag) {
			return nil, fmt.Errorf("unknown or duplicate collector")
		}
		args = append(args, flag)
	}
	scopeNeeded := slices.Contains(c.Collectors, "tables") || slices.Contains(c.Collectors, "collections") || slices.Contains(c.Collectors, "indexes")
	if scopeNeeded && len(c.Scope) == 0 {
		return nil, fmt.Errorf("collection or database scope is required")
	}
	for _, name := range c.Scope {
		if name == "*" && len(c.Scope) == 1 {
			continue
		}
		if len(name) > 200 || !scopePattern.MatchString(name) {
			return nil, fmt.Errorf("invalid collection or database name")
		}
	}

	scope := strings.Join(c.Scope, ",")
	if kind == KindMongoInstance && scopeNeeded {
		if scope == "*" {
			args = append(args, "--discovering-mode")
		} else {
			if slices.Contains(c.Collectors, "collections") {
				args = append(args, "--mongodb.collstats-colls="+scope)
			}
			if slices.Contains(c.Collectors, "indexes") {
				args = append(args, "--mongodb.indexstats-colls="+scope)
			}
		}
	}
	if slices.Contains(c.Collectors, "tables") {
		args = append(args, "--collect.info_schema.tables.databases="+scope)
	}
	if slices.Contains(c.Collectors, "statements") {
		args = append(args, "--collect.perf_schema.eventsstatements.limit=100")
	}
	return args, nil
}

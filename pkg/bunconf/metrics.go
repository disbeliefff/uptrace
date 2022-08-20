package bunconf

import (
	"fmt"
	"time"

	"github.com/uptrace/uptrace/pkg/metrics/alerting"
	"github.com/uptrace/uptrace/pkg/metrics/upql"
)

type Dashboard struct {
	ID      string                   `yaml:"id"`
	Name    string                   `yaml:"name"`
	Metrics []string                 `yaml:"metrics"`
	Query   []string                 `yaml:"query"`
	Columns map[string]*MetricColumn `yaml:"columns"`
	Entries []*DashEntry             `yaml:"entries"`
}

func (d *Dashboard) Validate() error {
	if d.ID == "" {
		return fmt.Errorf("template id is required")
	}
	if err := d.validate(); err != nil {
		return fmt.Errorf("invalid dashboard %s: %w", d.ID, err)
	}
	return nil
}

func (d *Dashboard) validate() error {
	if d.Name == "" {
		return fmt.Errorf("dashboard name is required")
	}
	if len(d.Query) == 0 && len(d.Entries) == 0 {
		return fmt.Errorf("either dashboard query or an entry is required")
	}

	for _, entry := range d.Entries {
		if err := entry.Validate(); err != nil {
			return err
		}
	}

	return nil
}

type DashEntry struct {
	Name    string                   `yaml:"name"`
	Metrics []string                 `yaml:"metrics"`
	Query   string                   `yaml:"query"`
	Columns map[string]*MetricColumn `yaml:"columns"`
}

func (e *DashEntry) Validate() error {
	if e.Name == "" {
		return fmt.Errorf("entry name is required")
	}
	if len(e.Metrics) == 0 {
		return fmt.Errorf("entry requires at least one metric")
	}
	if e.Query == "" {
		return fmt.Errorf("entry query is required")
	}
	return nil
}

type MetricColumn struct {
	Unit string `yaml:"unit" json:"unit"`
}

type AlertRule struct {
	Name        string            `yaml:"name"`
	Metrics     []string          `yaml:"metrics"`
	Expr        string            `yaml:"expr"`
	For         time.Duration     `yaml:"for"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
	Projects    []uint32          `yaml:"projects"`

	metrics []upql.Metric
}

func (r *AlertRule) Validate() error {
	if r.Name == "" {
		return fmt.Errorf("rule name is required")
	}
	if err := r.validate(); err != nil {
		return fmt.Errorf("invalid rule %q: %w", r.Name, err)
	}
	return nil
}

func (r *AlertRule) validate() error {
	if len(r.Metrics) == 0 {
		return fmt.Errorf("at least one metric is required")
	}

	metrics, err := upql.ParseMetrics(r.Metrics)
	if err != nil {
		return err
	}
	r.metrics = metrics

	if r.Expr == "" {
		return fmt.Errorf("rule expr is required")
	}
	if len(r.Projects) == 0 {
		return fmt.Errorf("at least on project is required")
	}
	return nil
}

func (r *AlertRule) RuleConfig() alerting.RuleConfig {
	return alerting.RuleConfig{
		Name:        r.Name,
		Metrics:     r.metrics,
		Expr:        r.Expr,
		For:         r.For,
		Labels:      r.Labels,
		Annotations: r.Annotations,
	}
}

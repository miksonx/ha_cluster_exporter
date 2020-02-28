package pacemaker

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/ClusterLabs/ha_cluster_exporter/collector"
	"github.com/ClusterLabs/ha_cluster_exporter/collector/pacemaker/cib"
	"github.com/ClusterLabs/ha_cluster_exporter/collector/pacemaker/crmmon"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

func NewCollector(crmMonPath string, cibAdminPath string) (*pacemakerCollector, error) {
	err := collector.CheckExecutables(crmMonPath, cibAdminPath)
	if err != nil {
		return nil, errors.Wrap(err, "could not initialize Pacemaker collector")
	}

	c := &pacemakerCollector{
		collector.NewDefaultCollector("pacemaker"),
		crmmon.NewCrmMonParser(crmMonPath),
		cib.NewCibAdminParser(cibAdminPath),
	}
	c.SetDescriptor("nodes", "The nodes in the cluster; one line per name, per status", []string{"node", "type", "status"})
	c.SetDescriptor("resources", "The resources in the cluster; one line per id, per status", []string{"node", "resource", "role", "managed", "status"})
	c.SetDescriptor("stonith_enabled", "Whether or not stonith is enabled", nil)
	c.SetDescriptor("fail_count", "The Fail count number per node and resource id", []string{"node", "resource"})
	c.SetDescriptor("migration_threshold", "The migration_threshold number per node and resource id", []string{"node", "resource"})
	c.SetDescriptor("config_last_change", "The timestamp of the last change of the cluster configuration", nil)
	c.SetDescriptor("location_constraints", "Resource location constraints. The value indicates the score.", []string{"constraint", "node", "resource", "role"})

	return c, nil
}

type pacemakerCollector struct {
	collector.DefaultCollector
	crmMonParser crmmon.Parser
	cibParser    cib.Parser
}

func (c *pacemakerCollector) Collect(ch chan<- prometheus.Metric) {
	log.Infoln("Collecting pacemaker metrics...")

	crmMon, err := c.crmMonParser.Parse()
	if err != nil {
		log.Warnln(err)
		return
	}

	CIB, err := c.cibParser.Parse()
	if err != nil {
		log.Warnln(err)
		return
	}

	var stonithEnabled float64
	if crmMon.Summary.ClusterOptions.StonithEnabled {
		stonithEnabled = 1
	}

	ch <- c.MakeGaugeMetric("stonith_enabled", stonithEnabled)

	c.recordNodes(crmMon, ch)
	c.recordUngroupedResources(crmMon, ch)
	c.recordFailCounts(crmMon, ch)
	c.recordMigrationThresholds(crmMon, ch)
	c.recordResourceAgentsChanges(crmMon, ch)
	c.recordConstraints(CIB, ch)
}

func (c *pacemakerCollector) recordNodes(crmMon crmmon.Root, ch chan<- prometheus.Metric) {
	for _, node := range crmMon.Nodes {
		var nodeType string
		switch node.Type {
		case "member", "ping", "remote":
			nodeType = node.Type
			break
		default:
			nodeType = "unknown"
		}

		// this is a map of boolean flags for each possible status of the node
		nodeStatuses := map[string]bool{
			"online":         node.Online,
			"standby":        node.Standby,
			"standby_onfail": node.StandbyOnFail,
			"maintenance":    node.Maintenance,
			"pending":        node.Pending,
			"unclean":        node.Unclean,
			"shutdown":       node.Shutdown,
			"expected_up":    node.ExpectedUp,
			"dc":             node.DC,
		}

		// since we have a combined cardinality of node * status, we cycle through all the possible statuses
		// and we record a new metric if the flag for that status is on
		for nodeStatus, flag := range nodeStatuses {
			if flag {
				ch <- c.MakeGaugeMetric("nodes", float64(1), node.Name, nodeType, nodeStatus)
			}
		}

		c.recordNodeResources(node, ch)
	}
}

func (c *pacemakerCollector) recordNodeResources(node crmmon.Node, ch chan<- prometheus.Metric) {
	nodeName := node.Name
	for _, resource := range node.Resources {
		c.recordResource(resource, nodeName, ch)
	}
}

func (c *pacemakerCollector) recordResource(resource crmmon.Resource, nodeName string, ch chan<- prometheus.Metric) {

	// this is a map of boolean flags for each possible status of the resource
	resourceStatuses := map[string]bool{
		"active":          resource.Active,
		"orphaned":        resource.Orphaned,
		"blocked":         resource.Blocked,
		"failed":          resource.Failed,
		"failure_ignored": resource.FailureIgnored,
		// if no status flag is active we record an empty status; probably a stopped resource, which is tracked in the "role" label instead
		"": !(resource.Active || resource.Orphaned || resource.Blocked || resource.Failed || resource.FailureIgnored),
	}

	// since we have a combined cardinality of resource * status, we cycle through all the possible statuses
	// and we record a new metric if the flag for that status is on
	for resourceStatus, flag := range resourceStatuses {
		if !flag {
			continue
		}
		ch <- c.MakeGaugeMetric(
			"resources",
			float64(1),
			nodeName,
			resource.Id,
			strings.ToLower(resource.Role),
			strconv.FormatBool(resource.Managed),
			resourceStatus)
	}
}

func (c *pacemakerCollector) recordUngroupedResources(crmMon crmmon.Root, ch chan<- prometheus.Metric) {
	for _, resource := range crmMon.Resources {
		c.recordResource(resource, "", ch)
	}
}

func (c *pacemakerCollector) recordFailCounts(crmMon crmmon.Root, ch chan<- prometheus.Metric) {
	for _, node := range crmMon.NodeHistory.Node {
		for _, resHistory := range node.ResourceHistory {
			failCount := float64(resHistory.FailCount)

			// if value is 1000000 this is a special value in pacemaker which is infinity fail count
			if resHistory.FailCount >= 1000000 {
				failCount = math.Inf(1)
			}

			ch <- c.MakeGaugeMetric("fail_count", failCount, node.Name, resHistory.Name)

		}
	}
}

func (c *pacemakerCollector) recordResourceAgentsChanges(crmMon crmmon.Root, ch chan<- prometheus.Metric) {
	t, err := time.Parse(time.ANSIC, crmMon.Summary.LastChange.Time)
	if err != nil {
		log.Warnln(err)
		return
	}
	// we record the timestamp of the last change as a float counter metric
	ch <- c.MakeCounterMetric("config_last_change", float64(t.Unix()))
}

func (c *pacemakerCollector) recordMigrationThresholds(crmMon crmmon.Root, ch chan<- prometheus.Metric) {
	for _, node := range crmMon.NodeHistory.Node {
		for _, resHistory := range node.ResourceHistory {
			ch <- c.MakeGaugeMetric("migration_threshold", float64(resHistory.MigrationThreshold), node.Name, resHistory.Name)
		}
	}
}

func (c *pacemakerCollector) recordConstraints(CIB cib.Root, ch chan<- prometheus.Metric) {
	for _, constraint := range CIB.Configuration.Constraints.RscLocations {
		var constraintScore float64
		switch constraint.Score {
		case "INFINITY":
			constraintScore = math.Inf(1)
		case "-INFINITY":
			constraintScore = math.Inf(-1)
		default:
			s, _ := strconv.Atoi(constraint.Score)
			constraintScore = float64(s)
		}

		ch <- c.MakeGaugeMetric("location_constraints", constraintScore, constraint.Id, constraint.Node, constraint.Resource, strings.ToLower(constraint.Role))
	}
}
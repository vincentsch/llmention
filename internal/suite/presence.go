package suite

import (
	"bytes"
	"strconv"

	"gopkg.in/yaml.v3"
)

func markItemSelectionPresence(data []byte, s *Suite) error {
	var root yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&root); err != nil {
		return err
	}
	doc := &root
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil
	}
	items := mappingValue(doc, "items")
	if items == nil || items.Kind != yaml.SequenceNode {
		return nil
	}
	for i, itemNode := range items.Content {
		if i >= len(s.Items) || itemNode.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(itemNode.Content); j += 2 {
			switch itemNode.Content[j].Value {
			case "evaluator_ids":
				s.Items[i].evaluatorIDsSet = true
			case "metric_ids":
				s.Items[i].metricIDsSet = true
			}
		}
	}
	return nil
}

func auditSourcePolicyPresenceIssues(data []byte, s *Suite) ([]Issue, error) {
	if s == nil || s.Type != "propaganda" {
		return nil, nil
	}
	var root yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	doc := &root
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		doc = root.Content[0]
	}
	if doc.Kind != yaml.MappingNode {
		return nil, nil
	}
	items := mappingValue(doc, "items")
	if items == nil || items.Kind != yaml.SequenceNode {
		return nil, nil
	}
	var issues []Issue
	for i, itemNode := range items.Content {
		if i >= len(s.Items) || itemNode.Kind != yaml.MappingNode {
			continue
		}
		path := "items[" + strconv.Itoa(i) + "].source_policy"
		policy := mappingValue(itemNode, "source_policy")
		if policy == nil {
			continue
		}
		if policy.Kind != yaml.MappingNode {
			issues = append(issues, Issue{Path: path, Message: "must be a group of fields, such as independent_sources_preferred and low_quality_domains"})
			continue
		}
		independent := mappingValue(policy, "independent_sources_preferred")
		if independent == nil {
			issues = append(issues, Issue{Path: path + ".independent_sources_preferred", Message: "must be written out, even when it is false"})
		} else if independent.Kind != yaml.ScalarNode || independent.Tag == "!!null" {
			issues = append(issues, Issue{Path: path + ".independent_sources_preferred", Message: "must be true or false"})
		}
		lowQuality := mappingValue(policy, "low_quality_domains")
		if lowQuality == nil {
			issues = append(issues, Issue{Path: path + ".low_quality_domains", Message: "must be written out, even when the list is empty"})
		} else if lowQuality.Kind != yaml.SequenceNode || lowQuality.Tag == "!!null" {
			issues = append(issues, Issue{Path: path + ".low_quality_domains", Message: "must be a list"})
		}
	}
	return issues, nil
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

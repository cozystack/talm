// Package yamltools provides functions for handling YAML nodes, such as copying comments, applying comments,
// and diffing YAML documents.
package yamltools

import (
	"bytes"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// CopyComments updates the comments in dstNode considering the structure of whitespace.
func CopyComments(srcNode, dstNode *yaml.Node, path string, dstPaths map[string]*yaml.Node) {
	if srcNode.HeadComment != "" || srcNode.LineComment != "" || srcNode.FootComment != "" {
		dstPaths[path] = srcNode
	}

	for i := 0; i < len(srcNode.Content); i++ {
		newPath := path + "/" + srcNode.Content[i].Value
		if srcNode.Kind == yaml.SequenceNode {
			newPath = path + "/" + strconv.Itoa(i)
		}
		CopyComments(srcNode.Content[i], dstNode, newPath, dstPaths)
	}
}

// ApplyComments applies the copied comments to the target document.
func ApplyComments(dstNode *yaml.Node, path string, dstPaths map[string]*yaml.Node) {
	if srcNode, ok := dstPaths[path]; ok {
		dstNode.HeadComment = mergeComments(dstNode.HeadComment, srcNode.HeadComment)
		dstNode.LineComment = mergeComments(dstNode.LineComment, srcNode.LineComment)
		dstNode.FootComment = mergeComments(dstNode.FootComment, srcNode.FootComment)
	}

	for i := 0; i < len(dstNode.Content); i++ {
		newPath := path + "/" + dstNode.Content[i].Value
		if dstNode.Kind == yaml.SequenceNode {
			newPath = path + "/" + strconv.Itoa(i)
		}
		ApplyComments(dstNode.Content[i], newPath, dstPaths)
	}
}

// mergeComments combines old and new comments considering empty lines.
func mergeComments(oldComment, newComment string) string {
	if oldComment == "" {
		return newComment
	}
	if newComment == "" {
		return oldComment
	}
	return strings.TrimSpace(oldComment) + "\n\n" + strings.TrimSpace(newComment)
}

// DiffYAMLs compares two YAML documents and outputs the differences.
func DiffYAMLs(original, modified []byte) ([]byte, error) {
	var origNode, modNode yaml.Node
	if err := yaml.Unmarshal(original, &origNode); err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(modified, &modNode); err != nil {
		return nil, err
	}

	clearComments(&origNode)
	clearComments(&modNode)

	diff := compareNodes(origNode.Content[0], modNode.Content[0])
	if diff == nil {
		return []byte{}, nil
	}

	buffer := &bytes.Buffer{}
	encoder := yaml.NewEncoder(buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(diff); err != nil {
		return nil, err
	}
	_ = encoder.Close()

	return buffer.Bytes(), nil
}

// clearComments cleans up comments in YAML nodes.
func clearComments(node *yaml.Node) {
	node.HeadComment = ""
	node.LineComment = ""
	node.FootComment = ""
	for _, n := range node.Content {
		clearComments(n)
	}
}

// compareNodes recursively finds differences between two YAML nodes.
func compareNodes(orig, mod *yaml.Node) *yaml.Node {
	if orig.Kind != mod.Kind {
		return mod
	}

	switch orig.Kind {
	case yaml.MappingNode:
		return compareMappingNodes(orig, mod)
	case yaml.SequenceNode:
		return compareSequenceNodes(orig, mod)
	case yaml.ScalarNode:
		if orig.Value != mod.Value {
			return mod
		}
	}
	return nil
}

func createDeleteNode() *yaml.Node {
	return &yaml.Node{
		Kind: yaml.MappingNode,
		Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Value: "$patch"},
			{Kind: yaml.ScalarNode, Value: "delete"},
		},
	}
}

// compareMappingNodes compares two mapping nodes and returns differences,
// prioritizing the order in the modified document but considering original document order where possible.
func compareMappingNodes(orig, mod *yaml.Node) *yaml.Node {
	diff := &yaml.Node{Kind: yaml.MappingNode}
	origMap := nodeMap(orig)
	modMap := nodeMap(mod)
	processedKeys := make(map[string]bool)

	// First pass: iterate over keys in the modified node
	for i := 0; i < len(mod.Content); i += 2 {
		key := mod.Content[i].Value
		modVal := modMap[key]
		origVal, origExists := origMap[key]

		if origExists {
			processedKeys[key] = true
			changedNode := compareNodes(origVal, modVal)
			if changedNode != nil {
				addNodeToDiff(diff, key, changedNode)
			}
		} else {
			addNodeToDiff(diff, key, modVal)
		}
	}

	// Second pass: add deletion directives for keys missing in the modified node
	for i := 0; i < len(orig.Content); i += 2 {
		key := orig.Content[i].Value
		if !processedKeys[key] {
			origVal := origMap[key]
			if origVal.Kind == yaml.MappingNode {
				nestedDelete := &yaml.Node{Kind: yaml.MappingNode}
				for j := 0; j < len(origVal.Content); j += 2 {
					nestedKey := origVal.Content[j].Value
					addNodeToDiff(nestedDelete, nestedKey, createDeleteNode())
				}
				addNodeToDiff(diff, key, nestedDelete)
			} else {
				addNodeToDiff(diff, key, createDeleteNode())
			}
		}
	}

	if len(diff.Content) == 0 {
		return nil
	}
	return diff
}

// compareSequenceNodes compares two sequence nodes and returns differences.
func compareSequenceNodes(orig, mod *yaml.Node) *yaml.Node {
	diff := &yaml.Node{Kind: yaml.SequenceNode}
	origSet := nodeSet(orig)
	for _, modItem := range mod.Content {
		if !origSet[modItem.Value] {
			diff.Content = append(diff.Content, modItem)
		}
	}

	if len(diff.Content) == 0 {
		return nil
	}
	return diff
}

// nodeSet creates a set of values from sequence nodes.
func nodeSet(node *yaml.Node) map[string]bool {
	result := make(map[string]bool)
	for _, item := range node.Content {
		result[item.Value] = true
	}
	return result
}

// addNodeToDiff adds a node to the diff result.
func addNodeToDiff(diff *yaml.Node, key string, node *yaml.Node) {
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	diff.Content = append(diff.Content, keyNode)
	diff.Content = append(diff.Content, node)
}

// nodeMap creates a map from a YAML mapping node for easy lookup.
func nodeMap(node *yaml.Node) map[string]*yaml.Node {
	result := make(map[string]*yaml.Node)
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		if keyNode.Kind == yaml.ScalarNode {
			result[keyNode.Value] = node.Content[i+1]
		}
	}
	return result
}

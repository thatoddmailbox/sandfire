package layercake

import (
	"fmt"
)

// LayerGraph manages the dependency graph of layers
type LayerGraph struct {
	layers   map[string]*Layer
	children map[string][]string // parent ID -> child IDs
}

// NewLayerGraph creates a new layer graph from a list of layers
func NewLayerGraph(layers []*Layer) (*LayerGraph, error) {
	g := &LayerGraph{
		layers:   make(map[string]*Layer),
		children: make(map[string][]string),
	}

	// Index layers by ID
	for _, layer := range layers {
		if _, exists := g.layers[layer.ID]; exists {
			return nil, fmt.Errorf("duplicate layer ID: %s", layer.ID)
		}
		g.layers[layer.ID] = layer
	}

	// Build children map and validate parents
	for _, layer := range layers {
		if layer.Parent == "scratch" {
			continue
		}
		if _, exists := g.layers[layer.Parent]; !exists {
			return nil, fmt.Errorf("layer %s has unknown parent %s", layer.ID, layer.Parent)
		}
		g.children[layer.Parent] = append(g.children[layer.Parent], layer.ID)
	}

	// Check for cycles
	if err := g.detectCycles(); err != nil {
		return nil, err
	}

	return g, nil
}

// detectCycles checks for circular dependencies
func (g *LayerGraph) detectCycles() error {
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	var visit func(id string) error
	visit = func(id string) error {
		visited[id] = true
		recStack[id] = true

		for _, childID := range g.children[id] {
			if !visited[childID] {
				if err := visit(childID); err != nil {
					return err
				}
			} else if recStack[childID] {
				return fmt.Errorf("circular dependency detected involving %s", childID)
			}
		}

		recStack[id] = false
		return nil
	}

	for id := range g.layers {
		if !visited[id] {
			if err := visit(id); err != nil {
				return err
			}
		}
	}

	return nil
}

// Get returns a layer by ID
func (g *LayerGraph) Get(id string) *Layer {
	return g.layers[id]
}

// All returns all layers
func (g *LayerGraph) All() []*Layer {
	result := make([]*Layer, 0, len(g.layers))
	for _, layer := range g.layers {
		result = append(result, layer)
	}
	return result
}

// TopologicalSort returns layers in build order (parents before children)
func (g *LayerGraph) TopologicalSort() []*Layer {
	var result []*Layer
	visited := make(map[string]bool)

	var visit func(id string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true

		layer := g.layers[id]
		if layer.Parent != "scratch" {
			visit(layer.Parent)
		}

		result = append(result, layer)
	}

	for id := range g.layers {
		visit(id)
	}

	return result
}

// GetBuildOrder returns layers needed to build the given layer, in order
// This includes all ancestors and the layer itself
func (g *LayerGraph) GetBuildOrder(id string) ([]*Layer, error) {
	layer := g.layers[id]
	if layer == nil {
		return nil, fmt.Errorf("unknown layer: %s", id)
	}

	var result []*Layer
	visited := make(map[string]bool)

	var visit func(id string)
	visit = func(id string) {
		if visited[id] {
			return
		}
		visited[id] = true

		l := g.layers[id]
		if l.Parent != "scratch" {
			visit(l.Parent)
		}
		result = append(result, l)
	}

	visit(id)
	return result, nil
}

// GetDescendants returns all descendants of a layer (for cascade rebuild)
func (g *LayerGraph) GetDescendants(id string) []*Layer {
	var result []*Layer
	visited := make(map[string]bool)

	var visit func(id string)
	visit = func(id string) {
		for _, childID := range g.children[id] {
			if !visited[childID] {
				visited[childID] = true
				result = append(result, g.layers[childID])
				visit(childID)
			}
		}
	}

	visit(id)
	return result
}

// GetBase returns the base layer (the one with PARENT=scratch)
func (g *LayerGraph) GetBase() *Layer {
	for _, layer := range g.layers {
		if layer.IsBase() {
			return layer
		}
	}
	return nil
}

// GetEffectiveRootfsSizeMB returns the effective rootfs size for a layer, taking inheritance into account.
// The effective size is the maximum of the layer's configured size and its parent's effective size,
// since a child layer's rootfs cannot be smaller than its parent's.
// If a layer specifies a value less than its parent's, a warning is printed.
// The default for base layers is 2048 MB.
func (g *LayerGraph) GetEffectiveRootfsSizeMB(id string) int {
	layer := g.layers[id]
	if layer == nil {
		return 2048
	}

	// For base layers, just return the configured size
	if layer.Parent == "scratch" {
		return layer.RootfsSizeMB
	}

	// Get parent's effective size
	parentSize := g.GetEffectiveRootfsSizeMB(layer.Parent)

	// Return the larger of parent and child sizes
	if layer.RootfsSizeMB > parentSize {
		return layer.RootfsSizeMB
	}

	// Warn if this layer specifies a smaller size than parent's effective size
	if layer.RootfsSizeMB < parentSize {
		fmt.Printf("Warning: layer %s has ROOTFS_SIZE_MB=%d which is less than parent's effective %d, using parent's value\n",
			layer.ID, layer.RootfsSizeMB, parentSize)
	}

	return parentSize
}

// GetEffectiveSuggestedRamMB returns the suggested RAM for a layer, taking inheritance into account.
// If a layer doesn't specify SUGGESTED_RAM_MB, it inherits from its parent.
// If a layer specifies a value less than its parent's, a warning is printed and the parent's value is used.
// The default for base layers without a specified value is 512 MB.
func (g *LayerGraph) GetEffectiveSuggestedRamMB(id string) int {
	layer := g.layers[id]
	if layer == nil {
		return 512
	}

	// Get parent's effective RAM (or default 512 for base layers)
	var parentRam int
	if layer.Parent == "scratch" {
		parentRam = 512
	} else {
		parentRam = g.GetEffectiveSuggestedRamMB(layer.Parent)
	}

	// If this layer doesn't specify a value, inherit from parent
	if layer.SuggestedRamMB == 0 {
		return parentRam
	}

	// If this layer specifies a value less than parent, warn and use parent's
	if layer.SuggestedRamMB < parentRam {
		fmt.Printf("Warning: layer %s has SUGGESTED_RAM_MB=%d which is less than parent's %d, using parent's value\n",
			layer.ID, layer.SuggestedRamMB, parentRam)
		return parentRam
	}

	return layer.SuggestedRamMB
}

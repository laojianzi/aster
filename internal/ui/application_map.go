package uiworkbench

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/laojianzi/aster/internal/health"
	"github.com/laojianzi/aster/internal/kube"
	"github.com/laojianzi/aster/internal/relationship"
	"github.com/laojianzi/aster/internal/resource"
	"k8s.io/apimachinery/pkg/types"
)

type applicationMap struct {
	busy     bool
	scroll   ui.ScrollState
	status   string
	nodes    []relationship.Link
	health   map[string]health.State
	edges    []applicationEdge
	warnings []string
}
type applicationEdge struct {
	from, to int
	relation string
}

func (w *Workbench) loadApplicationMap() {
	a := &w.applications
	if w.backend == nil || a.busy || a.selected == "" {
		return
	}
	if a.cancel != nil {
		a.cancel()
	}
	a.epoch++
	a.graph = applicationMap{busy: true, status: "Reading relationships…"}
	epoch, session, backend, selected, ns := a.epoch, w.sessionID, w.backend, a.selected, a.namespace
	var seeds []relationship.Link
	for _, item := range a.resources {
		if item.application == selected {
			seeds = append(seeds, relationship.Link{Target: resource.Identity{SessionID: session, GVR: item.kind.GVR, Namespace: item.row.Namespace, Name: item.row.Name, UID: types.UID(item.row.UID)}, Type: relationship.Type{GVR: item.kind.GVR, Kind: item.kind.Kind, Namespaced: item.kind.Namespaced}, State: relationship.Verified})
		}
	}
	var kinds []relationship.Type
	for _, k := range w.kinds {
		kinds = append(kinds, relationship.Type{GVR: k.GVR, Kind: k.Kind, Namespaced: k.Namespaced})
	}
	ctx, cancel := context.WithTimeout(w.operationContext(), 20*time.Second)
	a.cancel = cancel
	w.run(func(context.Context) {
		defer cancel()
		result := applicationMap{status: "Map ready", health: make(map[string]health.State)}
		indices := map[string]int{}
		for _, seed := range seeds[:min(len(seeds), 50)] {
			indices[seed.Target.Key()] = len(result.nodes)
			result.nodes = append(result.nodes, seed)
		}
		if len(seeds) > 50 {
			result.warnings = append(result.warnings, "Map node limit reached; use Members for the full application.")
		}
		requests, objects := 0, 0
		for i := 0; i < len(result.nodes); i++ {
			node := result.nodes[i]
			if !node.Navigable() || node.Target.GVR.Resource == "secrets" || node.Target.GVR.Resource == "configmaps" {
				continue
			}
			if requests >= 64 || objects >= 2000 || ctx.Err() != nil {
				result.warnings = append(result.warnings, "Map read limit reached; relationships are incomplete.")
				break
			}
			reader := relationship.New(backend, kinds, relationship.Limits{Requests: min(12, 64-requests), Objects: min(1000, 2000-objects)})
			snapshot, err := reader.Read(ctx, node.Target)
			result.health[node.Target.Key()] = snapshot.Health
			requests += snapshot.Requests
			objects += snapshot.Scanned
			result.warnings = append(result.warnings, snapshot.Warnings...)
			if err != nil {
				result.warnings = append(result.warnings, node.Type.Kind+"/"+node.Target.Name+": "+err.Error())
				continue
			}
			for _, link := range snapshot.Links {
				j, found := indices[link.Target.Key()]
				if !found {
					if len(result.nodes) >= 50 {
						result.warnings = append(result.warnings, "Map node limit reached; relationships are incomplete.")
						continue
					}
					j = len(result.nodes)
					indices[link.Target.Key()] = j
					result.nodes = append(result.nodes, link)
				}
				edge := applicationEdge{from: i, to: j, relation: link.Relation}
				switch link.Relation {
				case "Owned by", "Controlled by":
					edge.from, edge.to, edge.relation = j, i, "owns"
				case "Dependent":
					edge.relation = "owns"
				case "Selector match":
					edge.relation = "selects"
				}
				if !slices.Contains(result.edges, edge) {
					result.edges = append(result.edges, edge)
				}
			}
		}
		if len(result.warnings) > 0 {
			result.status = "Partial map"
			slices.Sort(result.warnings)
			result.warnings = slices.Compact(result.warnings)
		}
		w.emit(func() {
			a := &w.applications
			if a.epoch != epoch || w.sessionID != session || w.backend != backend || a.selected != selected || a.namespace != ns {
				return
			}
			a.graph = result
			a.cancel = nil
		})
	})
}

func (w *Workbench) applicationMapView(c *ui.Context) {
	a := &w.applications
	g := &a.graph
	t := c.Theme()
	scale := max(float32(1), c.Preferences().TextScale)
	ui.Row(c).Gap(8).Children(func() {
		ui.Text(c, a.selected).Bold()
		ui.Box(c).Grow(1)
		ui.Text(c, g.status).TextColor(t.TextMuted)
		ui.Button(c, "Refresh map").Disabled(g.busy).OnClick(w.loadApplicationMap)
	})
	if len(g.warnings) > 0 {
		ui.Collapsible(c, "Map read issues", &a.showMapIssues, func() { ui.Text(c, strings.Join(g.warnings, "\n")).Selectable() })
	}
	var edges []applicationEdge
	for _, edge := range g.edges {
		at := slices.IndexFunc(edges, func(existing applicationEdge) bool { return existing.from == edge.from && existing.to == edge.to })
		if at < 0 {
			edges = append(edges, edge)
		} else {
			edges[at].relation += ", " + edge.relation
		}
	}
	positions := make([][2]int, len(g.nodes))
	incoming := make([]int, len(g.nodes))
	owned := make([]bool, len(g.nodes))
	for _, edge := range g.edges {
		if edge.relation == "owns" {
			incoming[edge.to]++
			owned[edge.from], owned[edge.to] = true, true
		}
	}
	var queue []int
	for i, count := range incoming {
		if count == 0 {
			queue = append(queue, i)
		}
	}
	columns := 3
	for at := 0; at < len(queue); at++ {
		from := queue[at]
		for _, edge := range g.edges {
			if edge.from != from || edge.relation != "owns" {
				continue
			}
			positions[edge.to][0] = max(positions[edge.to][0], positions[from][0]+1)
			columns = max(columns, positions[edge.to][0]+2)
			incoming[edge.to]--
			if incoming[edge.to] == 0 {
				queue = append(queue, edge.to)
			}
		}
	}
	counts := make([]int, columns)
	for i, node := range g.nodes {
		col := positions[i][0]
		if !owned[i] {
			switch node.Type.Kind {
			case "Pod":
				col = columns - 2
			case "Service", "EndpointSlice", "ConfigMap", "Secret", "PersistentVolumeClaim", "ServiceAccount", "Node":
				col = columns - 1
			}
		}
		positions[i][0] = col
	}
	for col := range columns {
		var layer []int
		for i := range g.nodes {
			if positions[i][0] != col {
				continue
			}
			parentLayer := -1
			for _, edge := range g.edges {
				if edge.to == i && edge.relation == "owns" && positions[edge.from][0] < col && positions[edge.from][0] > parentLayer {
					parentLayer = positions[edge.from][0]
					positions[i][1] = positions[edge.from][1]
				}
			}
			layer = append(layer, i)
		}
		slices.SortStableFunc(layer, func(a, b int) int { return cmp.Compare(positions[a][1], positions[b][1]) })
		for _, i := range layer {
			positions[i][1] = max(counts[col], positions[i][1])
			counts[col] = positions[i][1] + 1
		}
	}
	pitch, height := 118*scale, 80*scale
	ui.Scroll(c.Key("application.map.scroll")).TrackScroll(&g.scroll).Grow(1).MinHeight(0).MinWidth(0).Children(func() {
		ui.Box(c.Key("application.map.canvas")).MinWidth(float32(max(640, columns*200))).Height(float32(max(1, slices.Max(counts)))*pitch + 20).Draw(func(p *ui.Painter, r ui.Rect) {
			step := r.W / float32(columns)
			for _, edge := range edges {
				from, to := positions[edge.from], positions[edge.to]
				x1, x2 := r.X+(float32(from[0])+0.45)*step, r.X+(float32(to[0])+0.45)*step
				y1, y2 := r.Y+12+float32(from[1])*pitch+height, r.Y+12+float32(to[1])*pitch+height
				path := new(ui.Path)
				if from[0] == to[0] {
					x1 = r.X + (float32(from[0])+0.9)*step
					x2 = x1
					y1 -= height / 2
					y2 -= height / 2
					bendX := r.X + float32(from[0]+1)*step - 8
					path.MoveTo(x1, y1).LineTo(bendX, y1).LineTo(bendX, y2).LineTo(x2, y2)
					path.MoveTo(x2+4, y2-3).LineTo(x2, y2).LineTo(x2+4, y2+3)
				} else {
					y1 -= height / 2
					y2 -= height / 2
					x1 = r.X + (float32(from[0])+0.9)*step
					x2 = r.X + float32(to[0])*step
					fromLane := r.X + float32(from[0]+1)*step - 8
					toLane := r.X + float32(to[0])*step - 8
					arrow := float32(-4)
					if from[0] > to[0] {
						x1 = r.X + float32(from[0])*step
						x2 = r.X + (float32(to[0])+0.9)*step
						fromLane = r.X + float32(from[0])*step - 8
						toLane = r.X + float32(to[0]+1)*step - 8
						arrow = 4
					}
					path.MoveTo(x1, y1).LineTo(fromLane, y1)
					if fromLane != toLane {
						bend := max(y1, y2) + height/2 + 16*scale
						path.LineTo(fromLane, bend).LineTo(toLane, bend)
					}
					path.LineTo(toLane, y2).LineTo(x2, y2)
					path.MoveTo(x2+arrow, y2-3).LineTo(x2, y2).LineTo(x2+arrow, y2+3)
				}
				p.StrokePath(path, 0.5, t.TextMuted)
			}
		}).Children(func() {
			type edgeLabel struct {
				column, row, source int
				text, description   string
			}
			var labels []edgeLabel
			for _, edge := range edges {
				from, to := positions[edge.from], positions[edge.to]
				column := max(from[0], to[0])
				if from[0] == to[0] {
					column++
				}
				row := to[1]
				if from[1] < to[1] {
					row--
				}
				if from[0]-to[0] > 1 || to[0]-from[0] > 1 {
					column = min(from[0], to[0]) + 1
					row = max(from[1], to[1])
				}
				description := g.nodes[edge.from].Type.Kind + "/" + g.nodes[edge.from].Target.Name + " " + edge.relation + " " + g.nodes[edge.to].Type.Kind + "/" + g.nodes[edge.to].Target.Name
				at := slices.IndexFunc(labels, func(label edgeLabel) bool {
					return label.source == edge.from && label.text == edge.relation || label.column == column && label.row == row
				})
				if at < 0 {
					labels = append(labels, edgeLabel{column, row, edge.from, edge.relation, description})
				} else {
					for _, relation := range strings.Split(edge.relation, ", ") {
						if !slices.Contains(strings.Split(labels[at].text, ", "), relation) {
							labels[at].text += ", " + relation
						}
					}
					labels[at].description += "\n" + description
				}
			}
			for _, label := range labels {
				ui.Text(c, label.text).Absolute().RightPercent(100-float32(label.column)*100/float32(columns)).Translate(-16, 0).Top(12 + float32(label.row)*pitch + height + 4).SingleLine().Label(label.description).Tooltip(label.description).TextColor(t.TextMuted)
			}

			for i, node := range g.nodes {
				pos := positions[i]
				name := node.Type.Kind + "/" + node.Target.Name
				state := g.health[node.Target.Key()]
				color := t.TextMuted
				switch state {
				case health.Ready, health.Completed:
					color = t.Success
				case health.Degraded, health.Failed:
					color = t.Danger
				case health.Progressing, health.Manual:
					color = t.Warning
				}
				label := string(state)
				if state == "" {
					label = string(health.Unknown)
				}
				if state == health.Unsupported {
					label = "Not assessed"
				}
				var item ui.Element
				if node.Navigable() {
					item = ui.ButtonBase(c.Key(node.Target.Key())).OnClick(func() {
						a.open = false
						w.openResourceKind(kube.ResourceKind{GVR: node.Type.GVR, Kind: node.Type.Kind, Namespaced: node.Type.Namespaced}, resourceRow{UID: string(node.Target.UID), Name: node.Target.Name, Namespace: node.Target.Namespace})
					})
				} else {
					item = ui.Row(c.Key(node.Target.Key())).Center().Focusable().FocusRing(true).Role(ui.RoleGroup).Shrink(0)
				}
				item.Justify(ui.Start).Absolute().LeftPercent(float32(pos[0])*100/float32(columns)).Top(12+float32(pos[1])*pitch).WidthPercent(90/float32(columns)).Height(height).Border(0.5, t.Border).Radius(4).Background(t.Surface).Padding(8).Label("Map " + name).Tooltip(name + "\nHealth: " + label + "\n" + string(node.State) + "\n" + node.Note).Children(func() {
					ui.Column(c).Gap(4).MinWidth(0).Children(func() {
						ui.Text(c, node.Type.Kind).TextColor(t.TextMuted).SingleLine()
						ui.Text(c, node.Target.Name).SingleLine()
						if node.State != relationship.Verified {
							ui.Text(c, string(node.State)).TextColor(t.TextMuted).SingleLine()
						} else {

							ui.Text(c, label).Label("Health " + name + ": " + label).SingleLine().TextColor(color)
						}
					})
				})
			}
		})
	})
}

package media

import "vartovyi/internal/model"

type Motion struct {
	Width, Height int
	background    []float64
	mask          []bool
	ready         bool
}

func NewMotion(w, h int, zones []model.Zone) *Motion {
	m := &Motion{Width: w, Height: h, background: make([]float64, w*h), mask: make([]bool, w*h)}
	hasInclude := false
	for _, z := range zones {
		if !z.Exclude {
			hasInclude = true
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := model.Point{X: (float64(x) + 0.5) / float64(w), Y: (float64(y) + 0.5) / float64(h)}
			inside := !hasInclude
			for _, z := range zones {
				if !z.Exclude && contains(z.Points, p) {
					inside = true
				}
			}
			for _, z := range zones {
				if z.Exclude && contains(z.Points, p) {
					inside = false
				}
			}
			m.mask[y*w+x] = inside
		}
	}
	return m
}
func contains(poly []model.Point, p model.Point) bool {
	inside := false
	j := len(poly) - 1
	for i := range poly {
		a, b := poly[i], poly[j]
		if (a.Y > p.Y) != (b.Y > p.Y) && p.X < (b.X-a.X)*(p.Y-a.Y)/(b.Y-a.Y)+a.X {
			inside = !inside
		}
		j = i
	}
	return inside
}

// Score adapts slowly to background and ignores changes under 25 grayscale levels.
func (m *Motion) Score(frame []byte) float64 {
	if len(frame) != len(m.background) {
		return 0
	}
	changed, total := 0, 0
	for i, b := range frame {
		v := float64(b)
		if !m.ready {
			m.background[i] = v
			continue
		}
		if m.mask[i] {
			total++
			d := v - m.background[i]
			if d > 25 || d < -25 {
				changed++
			}
		}
		m.background[i] = m.background[i]*0.98 + v*0.02
	}
	m.ready = true
	if total == 0 {
		return 0
	}
	return float64(changed) / float64(total)
}

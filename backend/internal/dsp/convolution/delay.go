package convolution

type delayLine struct {
	samples  []float64
	position int
}

func (d *delayLine) process(dst, src []float64) {
	if len(d.samples) == 0 {
		copy(dst, src)
		return
	}
	for i, v := range src {
		dst[i], d.samples[d.position] = d.samples[d.position], v
		d.position++
		if d.position == len(d.samples) {
			d.position = 0
		}
	}
}

func (d *delayLine) reset() {
	clear(d.samples)
	d.position = 0
}

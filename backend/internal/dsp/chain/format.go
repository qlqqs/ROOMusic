package chain

// DefaultLayout 返回 1–8 声道的标准布局；更多声道的未知布局保持为零。
func DefaultLayout(channels int) uint32 {
	layouts := [...]uint32{0, 0x4, 0x3, 0x7, 0x107, 0x607, 0x60f, 0x70f, 0x63f}
	if channels < 1 || channels >= len(layouts) {
		return 0
	}
	return layouts[channels]
}

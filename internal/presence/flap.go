package presence

// recordTransition 记录一次 online<->offline 切换，滑动窗口内超过阈值则进入 flapping。
func (e *Engine) recordTransition(mac string, now int64) {
	ds := e.states[mac]
	if ds == nil {
		return
	}
	// 抑制期结束后复位
	if ds.flapping && now >= ds.flapUntil {
		ds.flapping = false
		ds.transitions = nil
	}
	ds.transitions = append(ds.transitions, now)

	window := int64(e.cfg.Flap.Window.Value().Seconds())
	threshold := e.cfg.Flap.Threshold
	suppress := int64(e.cfg.Flap.Suppress.Value().Seconds())

	// 仅保留窗口内的切换
	kept := ds.transitions[:0]
	for _, t := range ds.transitions {
		if now-t <= window {
			kept = append(kept, t)
		}
	}
	ds.transitions = kept

	if len(ds.transitions) >= threshold {
		if !ds.flapping {
			ds.flapping = true
			// 仅在"进入 flapping"时设定抑制截止。
			// 原实现每次切换都续期 flapUntil，导致持续抖动的设备被永久静默
			// （只要切换间隔 < suppress 就再也收不到通知）。按设计抑制期应固定为 suppress。
			ds.flapUntil = now + suppress
			e.log.Warn("device flapping detected", "mac", mac, "transitions", len(ds.transitions))
			e.emit(Event{Type: TypeFlapping, MAC: mac, At: now, Flapping: true})
		}
	}
}

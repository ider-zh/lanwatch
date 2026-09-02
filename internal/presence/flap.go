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
			e.log.Warn("device flapping detected", "mac", mac, "transitions", len(ds.transitions))
			e.emit(Event{Type: TypeFlapping, MAC: mac, At: now, Flapping: true})
		}
		ds.flapUntil = now + suppress
	}
}

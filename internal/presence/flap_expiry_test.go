package presence

import (
	"context"
	"testing"
	"time"

	"github.com/ider-zh/lanwatch/internal/source"
)

// 回归：抖动抑制不能变成"永久静默"。
// 原实现在每次状态切换时都续期 flapUntil，导致持续抖动的设备
// （切换间隔 < suppress）永远等不到抑制期结束，再也收不到通知。
// 直接测 recordTransition：走 ProcessRound 时若命中 session_merge_gap 合并
// 会跳过 recordTransition，反而测不到这条路径。
func TestFlapSuppressionNotRatcheted(t *testing.T) {
	e, closeFn := newTestEngine(t, 4) // threshold=4
	defer closeFn()
	mac := "EE-EE-EE-EE-EE-EE"
	e.states[mac] = &deviceState{state: StOnline, hits: 1}

	now := time.Now().Unix()
	for i := 0; i < 4; i++ { // 4 次切换 -> 进入 flapping
		e.recordTransition(mac, now+int64(i))
	}
	ds := e.states[mac]
	if !ds.flapping {
		t.Fatal("expected device to be flapping after 4 transitions")
	}
	firstUntil := ds.flapUntil

	// 抑制期内继续切换：不应续期 flapUntil
	for i := 4; i < 8; i++ {
		e.recordTransition(mac, now+int64(i))
	}
	if ds.flapUntil != firstUntil {
		t.Fatalf("flapUntil extended on every transition (ratchet): %d -> %d", firstUntil, ds.flapUntil)
	}
}

// 抑制期结束后应恢复发事件，而不是白白吞掉恢复后的第一条。
func TestFlapSuppressionExpiresAndResumesNotify(t *testing.T) {
	e, closeFn := newTestEngine(t, 2)
	defer closeFn()
	ctx := context.Background()
	mac := "EE:EE:EE:EE:EE:EE"

	e.ProcessRound(ctx, round(map[string]bool{mac: true})) // online
	e.ProcessRound(ctx, round(map[string]bool{}))
	e.ProcessRound(ctx, round(map[string]bool{})) // offline -> flapping
	collect(e.Events())

	ds := e.states[source.NormalizeMAC(mac)] // states 以规范化后的 MAC 为键
	if ds == nil || !ds.flapping {
		t.Fatal("expected device to be flapping")
	}

	// 先让设备回到在线态（命中 merge-gap，不计切换），否则它已是 offline，
	// 后续 absent 轮次不会触发状态转移，也就测不到抑制是否解除。
	e.ProcessRound(ctx, round(map[string]bool{mac: true}))

	// 把抑制截止时间拨到过去，模拟时间流逝超过 suppress
	ds.flapUntil = time.Now().Unix() - 1
	e.ProcessRound(ctx, round(map[string]bool{}))
	e.ProcessRound(ctx, round(map[string]bool{}))

	if evs := collect(e.Events()); !hasType(evs, TypeOffline) {
		t.Fatal("expected offline event once suppression expired")
	}
}

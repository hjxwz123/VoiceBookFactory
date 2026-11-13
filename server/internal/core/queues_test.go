package core

import (
	"strings"
	"testing"

	"voicebook/internal/jobs"
	"voicebook/internal/model"
)

func TestTaskQueues(t *testing.T) {
	a, _ := testApp(t)
	a.Cfg.Workers = 6
	a.seedQueues()
	a.loadQueues()
	pools := a.Q.Pools()
	if len(pools) != 3 || pools[0].Size != 3 || pools[1].Size != 6 || pools[2].Size != 2 {
		t.Fatalf("default pools = %+v", pools)
	}
	var qs []model.JobQueue
	a.DB.Order("sort_order asc").Find(&qs)
	pipeline := qs[2]

	// restricting the only one-click queue to members would strand free users' runs
	_, err := a.SaveQueue(pipeline.ID, QueueInput{Name: "一键成书", Lanes: []string{jobs.LanePipeline}, Plans: []string{"team", "enterprise"}, Concurrency: 2, Enabled: true})
	if err == nil || !strings.Contains(err.Error(), "免费版") {
		t.Fatalf("expected a refusal naming the stranded plan, got %v", err)
	}
	// a members' queue on top of the shared one is fine, and members can then use both
	vip, err := a.SaveQueue("", QueueInput{Name: "会员加速", Lanes: []string{jobs.LanePipeline, "nonsense"}, Plans: []string{"team", "enterprise", "nobody"}, Concurrency: 4, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(vip.Lanes.V) != 1 || len(vip.Plans.V) != 2 {
		t.Fatalf("unknown kinds and plans were kept: %+v / %+v", vip.Lanes.V, vip.Plans.V)
	}
	if got := a.Q.Pools(); len(got) != 4 || got[3].Size != 4 {
		t.Fatalf("pools not reloaded: %+v", got)
	}
	view := a.QueueOverview()
	slots := map[string]int{}
	for _, row := range view["coverage"].([]map[string]any) {
		slots[row["plan"].(string)] = row[jobs.LanePipeline].(map[string]any)["slots"].(int)
	}
	if slots["free"] != 2 || slots["team"] != 6 {
		t.Fatalf("one-click slots per plan = %v", slots)
	}
	// now the shared queue may be limited to the plans the members' queue does not cover
	if _, err := a.SaveQueue(pipeline.ID, QueueInput{Name: "一键成书", Lanes: []string{jobs.LanePipeline}, Plans: []string{"free", "personal"}, Concurrency: 2, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// but the members' queue cannot then be removed or switched off
	if err := a.DeleteQueue(vip.ID); err == nil {
		t.Fatal("deleting the members' queue would strand team and enterprise")
	}
	if _, err := a.SaveQueue(vip.ID, QueueInput{Name: "会员加速", Lanes: []string{jobs.LanePipeline}, Plans: []string{"team", "enterprise"}, Concurrency: 4, Enabled: false}); err == nil {
		t.Fatal("disabling the members' queue would strand team and enterprise")
	}
	if _, err := a.SaveQueue(vip.ID, QueueInput{Name: "会员加速", Lanes: []string{jobs.LanePipeline}, Concurrency: 0, Enabled: true}); err == nil {
		t.Fatal("zero workers accepted")
	}
	if gaps := view["gaps"].([]QueueGap); len(gaps) != 0 {
		t.Fatalf("gaps = %v", gaps)
	}
}

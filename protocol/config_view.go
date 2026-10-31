package protocol

// TabInfo 描述一个选课页签（特殊课程 / 通识选修课 / 板块课…）。
// 它是 APIConfig 内部 modeStore 的只读视图，供上层展示与选择。
type TabInfo struct {
	Index  int    `json:"index"`
	Kklxmc string `json:"kklxmc"`
	Kklxdm string `json:"kklxdm"`
	XkkzXh string `json:"xkkzXh"`
	XkkzId string `json:"xkkzId"`
	NjdmId string `json:"njdmId"`
	ZyhId  string `json:"zyhId"`
}

// TabsOf 返回配置里的页签列表；没有页签时返回 nil。
func TabsOf(cfg *APIConfig) []TabInfo {
	if cfg == nil || len(cfg.Tabs.modeStore) == 0 {
		return nil
	}
	out := make([]TabInfo, 0, len(cfg.Tabs.modeStore))
	for i, item := range cfg.Tabs.modeStore {
		out = append(out, TabInfo{
			Index:  i,
			Kklxmc: item.Kklxmc,
			Kklxdm: item.Kklxdm,
			XkkzXh: item.Xkkz_xh,
			XkkzId: item.Xkkz_id,
			NjdmId: item.Njdm_id,
			ZyhId:  item.Zyh_id,
		})
	}
	return out
}

// ModeNameOf 返回配置当前的页签名。
func ModeNameOf(cfg *APIConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Tabs.modeName
}

// CurrentTabOf 返回配置当前选中的页签，以及是否找得到。
//
// 为什么不能只看 ModeNameOf：重抓首页参数后 modeName 会被页面地址栏里的默认
// 页签覆盖，engine 要在刷新前记下"用户选的是哪个页签"，刷新后再用新页面里的
// xkkz_id 切回去（见 engine.session.Renew）。
//
// 判定顺序：课程类型代码 + xkkz_id 都对上最可靠（SelectTab 就是按这个写回的）；
// 其次只比课程类型代码（默认页签的 xkkz_id 不一定等于 firstXkkzId）；最后才
// 按页签名比。页签列表为空时返回 false。
func CurrentTabOf(cfg *APIConfig) (TabInfo, bool) {
	if cfg == nil || len(cfg.Tabs.modeStore) == 0 {
		return TabInfo{}, false
	}
	tabs := TabsOf(cfg)
	for _, tab := range tabs {
		if tab.Kklxdm != "" && tab.Kklxdm == cfg.Index.kklxdm && tab.XkkzId == cfg.Index.xkkz_id {
			return tab, true
		}
	}
	for _, tab := range tabs {
		if tab.Kklxdm != "" && tab.Kklxdm == cfg.Index.kklxdm {
			return tab, true
		}
	}
	for _, tab := range tabs {
		if tab.Kklxmc != "" && tab.Kklxmc == cfg.Tabs.modeName {
			return tab, true
		}
	}
	return TabInfo{}, false
}

// WithTab 返回一份把 tab 应用到 Index/Tabs 上的配置副本。
//
// 调用方（engine）拿到的是新副本，旧快照仍然完整——切页签失败时不需要回滚。
func WithTab(cfg *APIConfig, tab TabInfo) *APIConfig {
	if cfg == nil {
		cfg = &APIConfig{}
	}
	c := *cfg
	c.Tabs.modeName = tab.Kklxmc
	c.Index.kklxdm = tab.Kklxdm
	c.Index.xkkz_id = tab.XkkzId
	c.Index.xkkz_xh = tab.XkkzXh
	if tab.XkkzXh != "" {
		c.Index.njdm_id = tab.NjdmId
		c.Index.njdm_id_list0 = tab.NjdmId
		c.Index.zyh_id = tab.ZyhId
	}
	return &c
}

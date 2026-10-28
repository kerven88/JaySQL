package forms

// ---------------------------------------------------------------------------
// 主机登录（数据源总览 → 操作列「主机登录」）
//
// 交互形态是「新开窗口的独立终端页」，因此接口分成三步：
//   1. precheck  终端页加载后自检：凭据是否齐备、主机是否可达（不建立交互式会话）
//   2. sessions  签发一次性令牌，换 ws_url
//   3. ws        WebSocket + SSH PTY 交互
// 三步都只传 instance_id，凭据一律不出服务端。
// ---------------------------------------------------------------------------

type HostLoginPrecheckForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required"`
}

type CreateHostLoginSessionForm struct {
	InstanceID string `form:"instance_id" json:"instance_id" binding:"required"`
	// 上一次会话记录的主机密钥指纹（浏览器侧持久化）。为空表示首次连接（TOFU：接受并回传指纹）；
	// 与服务端算出的指纹不一致时阻断建连，由用户显式确认后才放行。
	HostKeyFingerprint string `form:"host_key_fingerprint" json:"host_key_fingerprint"`
}

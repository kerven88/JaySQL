package services

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/simajiong/jaysql/internal/global"
	"golang.org/x/crypto/ssh"
)

// ---------------------------------------------------------------------------
// 主机终端：WebSocket ↔ SSH PTY
//
// 帧协议（文本 JSON，便于排查；终端字节流放在 d 字段里）：
//   C→S: {"t":"in","d":"ls\n"} / {"t":"resize","cols":120,"rows":32} / {"t":"ping"}
//   S→C: {"t":"out","d":"..."} / {"t":"ready","fingerprint":"...","no_pty":true,"os_hint":"windows"} /
//        {"t":"exit","code":"LOGOUT","reason":"..."} / {"t":"pong"}
//
// ready 帧的两个附加字段都只影响提示文案：
//   - no_pty：主机拒绝了 pty-req（Windows OpenSSH 的老版本 / 受限镜像常见），已降级为无 PTY 的 shell；
//   - os_hint：由服务端自报标识粗判的操作系统，用来提示代码页一类的本地化差异。
// ---------------------------------------------------------------------------

// 与工单模块（orders/views/ws.go）保持一致：内网系统，不对 Origin 做限制，
// 否则经 Nginx 反向代理后 Origin 与 Host 不一致会 403。
var hostLoginUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type wsInbound struct {
	T    string `json:"t"`
	D    string `json:"d"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type wsOutbound struct {
	T           string `json:"t"`
	D           string `json:"d,omitempty"`
	Code        string `json:"code,omitempty"`
	Reason      string `json:"reason,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// 仅在 ready 帧出现
	NoPty  bool   `json:"no_pty,omitempty"`
	OSHint string `json:"os_hint,omitempty"`
}

// ---------------------------------------------------------------------------
// PTY
// ---------------------------------------------------------------------------

type HostLoginPty struct {
	client   *ssh.Client
	session  *ssh.Session
	stdin    io.WriteCloser
	hasPty   bool // 是否真的申请到了 PTY（false = 降级为无 PTY 的 shell）
	closeOne sync.Once
}

// newHostLoginPty 申请 PTY 并挂上 shell。终端类型按回退链选择：
// 精简镜像（国产 OS 的服务器版、最小化安装的 Debian）没有 xterm-256color 的 terminfo，
// 直接用会报 unknown terminal type，vi / top 直接不可用。
//
// PTY 申请失败时不再整体失败，而是降级为「无 PTY 的 shell」：
// Windows OpenSSH 的 pty-req 依赖 conpty，老版本 / 受限镜像会直接拒绝，但 cmd / PowerShell
// 走普通管道依然可用 —— 一行命令都敲不了的失败，比窗口尺寸不同步严重得多。
// 降级事实通过 hasPty 传给 ready 帧，由前端明确告知用户哪些能力没了。
//
// 输出直接把 WS 写手挂到 Session.Stdout/Stderr 上，而不是事后 StdoutPipe：
// StdoutPipe 必须在 Shell() 之前取，包进 helper 里很容易写反，写反了就是一个字节都收不到。
func newHostLoginPty(client *ssh.Client, rows, cols uint16, out io.Writer) (*HostLoginPty, error) {
	sess, err := client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("创建 SSH 会话失败: %w", err)
	}
	modes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	hasPty := true
	if ptyErr := requestPtyWithFallback(sess, rows, cols, modes); ptyErr != nil {
		hasPty = false
		global.App.Log.Warnf("host-login 申请 PTY 失败，降级为无 PTY 的 shell: host=%s error=%v",
			client.RemoteAddr().String(), ptyErr)
	}
	stdin, err := sess.StdinPipe()
	if err != nil {
		_ = sess.Close()
		return nil, fmt.Errorf("打开 SSH 标准输入失败: %w", err)
	}
	sess.Stdout = out
	sess.Stderr = out
	if err := sess.Shell(); err != nil {
		_ = stdin.Close()
		_ = sess.Close()
		return nil, fmt.Errorf("启动 shell 失败: %w", err)
	}
	return &HostLoginPty{client: client, session: sess, stdin: stdin, hasPty: hasPty}, nil
}

// HasPty false 表示当前是无 PTY 的降级会话：窗口尺寸同步与全屏程序都不可用。
func (p *HostLoginPty) HasPty() bool {
	return p != nil && p.hasPty
}

func requestPtyWithFallback(sess *ssh.Session, rows, cols uint16, modes ssh.TerminalModes) error {
	for _, term := range []string{hostLoginTermPrimary, hostLoginTermFallback1, hostLoginTermFallback2} {
		if err := sess.RequestPty(term, int(rows), int(cols), modes); err == nil {
			return nil
		}
	}
	return fmt.Errorf("申请 PTY 失败：主机不支持可用的终端类型")
}

func (p *HostLoginPty) Write(data []byte) error {
	if p == nil || p.stdin == nil {
		return fmt.Errorf("PTY 未就绪")
	}
	_, err := p.stdin.Write(data)
	return err
}

func (p *HostLoginPty) Resize(rows, cols uint16) error {
	// 无 PTY 时 WindowChange 没有接收方，发了对端只会报错，直接跳过
	if p == nil || p.session == nil || !p.hasPty {
		return nil
	}
	return p.session.WindowChange(int(rows), int(cols))
}

func (p *HostLoginPty) Close() {
	if p == nil {
		return
	}
	p.closeOne.Do(func() {
		if p.stdin != nil {
			_ = p.stdin.Close()
		}
		if p.session != nil {
			_ = p.session.Close()
		}
		if p.client != nil {
			_ = p.client.Close()
		}
	})
}

// ---------------------------------------------------------------------------
// WebSocket 会话
// ---------------------------------------------------------------------------

// wsTerminalWriter 把 SSH 输出逐块转发给 WS。WS 连接不支持并发写，这里串行化。
type wsTerminalWriter struct {
	mu sync.Mutex
	ws *websocket.Conn
}

func (w *wsTerminalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ws == nil {
		return len(p), nil
	}
	if err := w.ws.WriteJSON(wsOutbound{T: "out", D: string(p)}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (w *wsTerminalWriter) writeJSON(v wsOutbound) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ws == nil {
		return nil
	}
	return w.ws.WriteJSON(v)
}

// closeWS 主动掐掉 WebSocket。
// 守护协程必须调它：readHostLoginInput 是阻塞在 ReadMessage 上的，
// 只发 exit 帧不关连接的话，那个协程会一直挂在读上，直到客户端自己断开才被回收。
// gorilla/websocket 允许 Close 与读并发调用。
func (w *wsTerminalWriter) closeWS() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ws != nil {
		_ = w.ws.Close()
	}
}

// ServeHostLoginTerminal 处理一次 WS 会话，直到终端退出或连接断开。
func ServeHostLoginTerminal(c *gin.Context, sessionID, token string) {
	session, ok := hostLoginSessions.take(sessionID, token)
	if !ok {
		// 令牌已作废（过期或已被使用过）：刷新页面必然走到这里
		c.JSON(http.StatusUnauthorized, gin.H{"code": "0001", "message": "会话令牌无效或已过期"})
		return
	}
	target, ok := hostLoginTargets.Load(sessionID)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"code": "0001", "message": "会话已失效"})
		return
	}

	ws, err := hostLoginUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		global.App.Log.Errorf("host-login ws upgrade failed: session=%s error=%v", sessionID, err)
		cleanupHostLoginSession(session)
		return
	}
	defer ws.Close()

	session.WS = ws
	writer := &wsTerminalWriter{ws: ws}

	client, fingerprint, dialErr := dialHostLoginSSH(target)
	if dialErr != nil {
		_ = writer.writeJSON(wsOutbound{T: "exit", Code: dialErr.code, Reason: dialErr.message})
		// 没有 PTY 就没有别的收尾动作，留着 WS 会让前端一直停在「连接中」
		writer.closeWS()
		hostLoginTargets.Delete(sessionID)
		cleanupHostLoginSession(session)
		TryWriteAuditLog(c, AuditLogEvent{
			Username:       session.Username,
			AuditLevel:     "WARNING",
			ModuleCode:     "datasource",
			ModuleName:     "数据源管理",
			ActionCode:     "datasource.host_login",
			ActionName:     "主机登录",
			ActionResult:   "fail",
			ResourceType:   "instance",
			ResourceID:     session.InstanceID,
			ResourceName:   target.Name,
			Summary:        fmt.Sprintf("主机登录失败：%s（%s）", dialErr.message, session.Label),
			ErrorMessage:   dialErr.detail,
			RequestPayload: hostLoginRequestPayload(target),
		})
		return
	}

	pty, ptyErr := newHostLoginPty(client, 32, 120, writer)
	if ptyErr != nil {
		_ = client.Close()
		_ = writer.writeJSON(wsOutbound{T: "exit", Code: HostLoginErrAlgorithmMismatch, Reason: ptyErr.Error()})
		writer.closeWS()
		hostLoginTargets.Delete(sessionID)
		cleanupHostLoginSession(session)
		return
	}
	// TOFU：主机密钥变了必须断在 PTY 之前。宁可让用户多确认一次，
	// 也不能默默连上一台换过指纹的主机（重装是偶然，中间人是必然要防的）
	if expected := session.ExpectedFingerprint; expected != "" && fingerprint != "" && fingerprint != expected {
		pty.Close()
		_ = writer.writeJSON(wsOutbound{
			T:      "exit",
			Code:   HostLoginErrHostKeyChanged,
			Reason: fmt.Sprintf("主机密钥指纹与上次不一致（原 %s，新 %s）。可能是主机重装，也可能存在中间人风险，已断开连接。", expected, fingerprint),
		})
		writer.closeWS()
		hostLoginTargets.Delete(sessionID)
		cleanupHostLoginSession(session)
		return
	}
	session.SSHPty = pty
	// 目标信息只在建连阶段需要，明文口令在此刻起不再保留
	hostLoginTargets.Delete(sessionID)

	// 审计：建连成功
	TryWriteAuditLog(c, AuditLogEvent{
		Username:       session.Username,
		AuditLevel:     "INFO",
		ModuleCode:     "datasource",
		ModuleName:     "数据源管理",
		ActionCode:     "datasource.host_login",
		ActionName:     "主机登录",
		ActionResult:   "success",
		ResourceType:   "instance",
		ResourceID:     session.InstanceID,
		ResourceName:   target.Name,
		Summary:        fmt.Sprintf("主机登录：%s", session.Label),
		RequestPayload: hostLoginRequestPayload(target),
	})

	readyFrame := wsOutbound{T: "ready", Fingerprint: fingerprint, NoPty: !pty.HasPty()}
	// os_hint 只挑 Windows：其它系统的差异常规终端都能自洽，没必要给提示
	if hostLoginServerLooksWindows(string(client.ServerVersion())) {
		readyFrame.OSHint = "windows"
	}
	_ = writer.writeJSON(readyFrame)

	// shell 自然结束时收尾（用户输入 exit）
	go waitHostLoginShell(pty, writer, session)
	// 生命周期守护：空闲 30 分钟 / 总时长 2 小时
	go guardHostLoginSession(session, writer)

	exitCode, reason := readHostLoginInput(ws, pty, session)

	cleanupHostLoginSession(session)

	// 审计：结束（区分主动退出与异常中断）
	duration := time.Since(session.CreatedAt)
	result := "success"
	if exitCode == "INTERRUPTED" {
		result = "fail"
	}
	TryWriteAuditLog(c, AuditLogEvent{
		Username:       session.Username,
		AuditLevel:     "INFO",
		ModuleCode:     "datasource",
		ModuleName:     "数据源管理",
		ActionCode:     "datasource.host_login_end",
		ActionName:     "主机登录结束",
		ActionResult:   result,
		ResourceType:   "instance",
		ResourceID:     session.InstanceID,
		ResourceName:   target.Name,
		Summary:        fmt.Sprintf("主机登录结束：%s，用时 %s，退出原因 %s", session.Label, duration.Round(time.Second), exitCode),
		ErrorMessage:   reason,
		RequestPayload: map[string]any{"exit_code": exitCode, "duration_seconds": int(duration.Seconds())},
	})
}

// waitHostLoginShell 等 shell 自然结束（用户在终端里敲了 exit，或命令执行完）。
// 结束后必须通知前端并回收会话，否则窗口会一直挂着一个已经不存在的终端。
func waitHostLoginShell(pty *HostLoginPty, writer *wsTerminalWriter, session *HostLoginSession) {
	waitErr := pty.session.Wait()
	// 用户 exit 出来的状态码 0 是正常退出，不是错误
	if waitErr != nil && !strings.Contains(waitErr.Error(), "exit status 0") {
		_ = writer.writeJSON(wsOutbound{T: "exit", Code: "INTERRUPTED", Reason: "远程 shell 已结束。"})
	} else {
		_ = writer.writeJSON(wsOutbound{T: "exit", Code: "LOGOUT", Reason: "远程 shell 已退出。"})
	}
	session.close()
	writer.closeWS()
}

// readHostLoginInput WS → PTY。返回退出码与说明。
func readHostLoginInput(ws *websocket.Conn, pty *HostLoginPty, session *HostLoginSession) (string, string) {
	for {
		select {
		case <-session.closeCh:
			return "CLOSED", "会话已被回收"
		default:
		}
		_, msg, err := ws.ReadMessage()
		if err != nil {
			// 读失败 = 客户端断了：可能是关窗口，也可能是网络中断，都按中断处理
			return "INTERRUPTED", err.Error()
		}
		var in wsInbound
		if jsonErr := json.Unmarshal(msg, &in); jsonErr != nil {
			continue
		}
		session.touch()
		switch in.T {
		case "ping":
			_ = ws.WriteJSON(wsOutbound{T: "pong"})
		case "in":
			if in.D != "" {
				if writeErr := pty.Write([]byte(in.D)); writeErr != nil {
					return "INTERRUPTED", writeErr.Error()
				}
			}
		case "resize":
			cols, rows := in.Cols, in.Rows
			if cols == 0 {
				cols = 120
			}
			if rows == 0 {
				rows = 32
			}
			_ = pty.Resize(rows, cols)
		}
	}
}

// guardHostLoginSession 空闲与总时长守护。到点后主动关闭，避免无限期占用主机连接。
func guardHostLoginSession(session *HostLoginSession, writer *wsTerminalWriter) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-session.closeCh:
			return
		case <-ticker.C:
			session.mu.Lock()
			last := session.LastActive
			created := session.CreatedAt
			session.mu.Unlock()
			now := time.Now()
			if now.Sub(last) >= hostLoginIdleTimeout {
				_ = writer.writeJSON(wsOutbound{T: "exit", Code: "IDLE_TIMEOUT", Reason: "会话因连续无操作已自动断开。"})
				session.close()
				writer.closeWS()
				return
			}
			if now.Sub(created) >= hostLoginTotalTimeout {
				_ = writer.writeJSON(wsOutbound{T: "exit", Code: "SESSION_EXPIRED", Reason: "会话已到期（最长 2 小时），请重新登录。"})
				session.close()
				writer.closeWS()
				return
			}
		}
	}
}

func cleanupHostLoginSession(session *HostLoginSession) {
	session.close()
	hostLoginSessions.remove(session.ID)
	hostLoginTargets.Delete(session.ID)
	global.App.Log.Infof("host-login session closed: id=%s user=%s target=%s", session.ID, session.Username, session.Label)
}

// CloseHostLoginSession 供主动断开接口调用（幂等：已关闭的会话直接返回）。
func CloseHostLoginSession(sessionID, username string) bool {
	session, ok := hostLoginSessions.get(sessionID)
	if !ok {
		return false
	}
	// 会话归属校验：只能关自己的
	if username != "" && session.Username != username {
		return false
	}
	cleanupHostLoginSession(session)
	return true
}

// 终端页用的活跃会话列表（并发上限提示里的「查看我的终端」）
type HostLoginSessionBrief struct {
	SessionID  string `json:"session_id"`
	InstanceID string `json:"instance_id"`
	Label      string `json:"label"`
	CreatedAt  string `json:"created_at"`
}

func ListHostLoginSessions(username string) []HostLoginSessionBrief {
	list := hostLoginSessions.listByUser(username)
	out := make([]HostLoginSessionBrief, 0, len(list))
	for _, s := range list {
		out = append(out, HostLoginSessionBrief{
			SessionID:  s.ID,
			InstanceID: s.InstanceID,
			Label:      s.Label,
			CreatedAt:  s.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return out
}

// 供 view 层判断：保持内部类型不外泄
func HostLoginSessionExists(sessionID string) bool {
	_, ok := hostLoginSessions.get(sessionID)
	return ok
}

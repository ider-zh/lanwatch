package asus

import (
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// HostKeyStore 主机密钥持久化（防中间人）。首次连接信任并记录，之后变更则拒绝。
type HostKeyStore interface {
	Known(hostport string) (fp string, ok bool)
	Record(hostport, fp string) error
}

// SSHRunner 抽象命令执行，便于测试注入 fake。
type SSHRunner interface {
	Run(script string) (string, error)
	Close() error
}

type sshRunner struct {
	addr      string
	user      string
	pass      string
	hks       HostKeyStore
	keepalive time.Duration
	dialTO    time.Duration

	mu        sync.Mutex
	client    *ssh.Client
	closed    bool
	kaStarted bool
}

// NewSSHRunner 构建 runner；不立即拨号（延迟到首次 Run，以便启动时源不可用也不致命）。
func NewSSHRunner(addr, user, pass string, hks HostKeyStore, keepalive, dialTO time.Duration) (SSHRunner, error) {
	return &sshRunner{addr: addr, user: user, pass: pass, hks: hks, keepalive: keepalive, dialTO: dialTO}, nil
}

func (r *sshRunner) hostKeyCallback() ssh.HostKeyCallback {
	return func(hostport string, _ net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		if known, ok := r.hks.Known(hostport); ok {
			if known != fp {
				return fmt.Errorf("host key mismatch for %s: known=%s new=%s (possible MITM)", hostport, known, fp)
			}
			return nil
		}
		return r.hks.Record(hostport, fp)
	}
}

func (r *sshRunner) redial() error {
	cfg := &ssh.ClientConfig{
		User:            r.user,
		Auth:            []ssh.AuthMethod{ssh.Password(r.pass)},
		HostKeyCallback: r.hostKeyCallback(),
		Timeout:         r.dialTO,
	}
	client, err := ssh.Dial("tcp", r.addr, cfg)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", r.addr, err)
	}
	r.client = client
	return nil
}

func (r *sshRunner) Run(script string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return "", fmt.Errorf("runner closed")
	}
	if r.client == nil {
		if err := r.redial(); err != nil {
			return "", err
		}
		if r.keepalive > 0 && !r.kaStarted {
			r.kaStarted = true
			go r.keepaliveLoop()
		}
	}
	session, err := r.client.NewSession()
	if err != nil {
		// 连接可能已断，尝试重连一次
		_ = r.client.Close()
		r.client = nil
		if rerr := r.redial(); rerr != nil {
			return "", err
		}
		if r.keepalive > 0 && !r.kaStarted {
			r.kaStarted = true
			go r.keepaliveLoop()
		}
		session, err = r.client.NewSession()
		if err != nil {
			return "", err
		}
	}
	defer session.Close()
	out, err := session.CombinedOutput(script)
	return string(out), err
}

func (r *sshRunner) keepaliveLoop() {
	if r.keepalive <= 0 {
		return
	}
	t := time.NewTicker(r.keepalive)
	defer t.Stop()
	for range t.C {
		r.mu.Lock()
		if r.closed || r.client == nil {
			r.mu.Unlock()
			return
		}
		client := r.client
		r.mu.Unlock()
		_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
		if err != nil {
			r.mu.Lock()
			if !r.closed {
				_ = client.Close()
				r.client = nil
			}
			r.mu.Unlock()
		}
	}
}

func (r *sshRunner) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}

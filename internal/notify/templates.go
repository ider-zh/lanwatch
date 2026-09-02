package notify

import (
	"bytes"
	"text/template"
)

var (
	dingtalkTpl = template.Must(template.New("dingtalk").Parse(`### {{if eq .Event.Type "offline"}}设备离线{{else if eq .Event.Type "online"}}设备上线{{else if eq .Event.Type "new_device"}}新设备接入{{else}}{{.Event.TypeCN}}{{end}}
- **设备**：{{.Device.NameOrHost}}（{{.Device.IP}} · {{.Device.MAC}}）
{{- if .Device.Vendor}}
- **厂商**：{{.Device.Vendor}}{{end}}
{{- if .Device.SSID}}
- **接入**：{{.Device.LinkTypeCN}} / {{.Device.SSID}}{{end}}
- **时间**：{{.Event.At.Format "2006-01-02 15:04:05"}}
{{- if eq .Event.Type "offline"}}
- **已离线**：{{.Duration}}（上次在线 {{.LastSeen}}）{{end}}
{{- if .Flapping}}
- ⚠️ 该设备短时间内反复上下线，后续通知已临时抑制{{end}}
- **来源**：{{.SourceName}}`))

	emailSubjectTpl = template.Must(template.New("emailSubject").Parse(`[lanwatch] {{.Event.TypeCN}} — {{.Device.NameOrHost}}`))

	emailBodyTpl = template.Must(template.New("emailBody").Parse(`<div style="font-family:-apple-system,Segoe UI,sans-serif">
  <h3 style="margin:0 0 12px">{{.Event.TypeCN}}：{{.Device.NameOrHost}}</h3>
  <table cellpadding="4" style="border-collapse:collapse;font-size:14px">
    <tr><td style="color:#888">MAC</td><td><code>{{.Device.MAC}}</code></td></tr>
    <tr><td style="color:#888">IP</td><td>{{.Device.IP}}</td></tr>
    <tr><td style="color:#888">接入方式</td><td>{{.Device.LinkTypeCN}}{{if .Device.SSID}} / {{.Device.SSID}}{{end}}</td></tr>
    <tr><td style="color:#888">发生时间</td><td>{{.Event.At.Format "2006-01-02 15:04:05"}}</td></tr>
    {{if eq .Event.Type "offline"}}
    <tr><td style="color:#888">持续离线</td><td>{{.Duration}}</td></tr>
    {{end}}
    <tr><td style="color:#888">采集源</td><td>{{.SourceName}}</td></tr>
  </table>
  <p style="color:#aaa;font-size:12px;margin-top:16px">
    本邮件由 lanwatch 自动发送 · <a href="{{.BaseURL}}/timeline">查看在线区间</a>
  </p>
</div>`))
)

func renderDingtalk(data TemplateData) (title, text string, err error) {
	title = data.Event.TypeCN()
	var buf bytes.Buffer
	if err := dingtalkTpl.Execute(&buf, data); err != nil {
		return "", "", err
	}
	return title, buf.String(), nil
}

func renderEmailSubject(data TemplateData) string {
	var buf bytes.Buffer
	_ = emailSubjectTpl.Execute(&buf, data)
	return buf.String()
}

func renderEmailHTML(data TemplateData) (string, error) {
	var buf bytes.Buffer
	if err := emailBodyTpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

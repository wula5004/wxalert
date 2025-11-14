package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "sync"
    "text/template"
    "time"
)


const pushPlusTokenHardcoded = "257c7c06da1047eea5c5f5a250487d46"
const pushPlusTopicHardcoded = "20251105"
const httpAddr = "10.0.0.200:18080"
// 可选：固定内容模板（不使用环境变量）。留空则使用默认拼装。
// 模板示例：'[{{.level}}] {{.subject}} | {{.service}} - {{.msg}} ({{.ip}})'
const contentTemplateHardcoded = ""
// 负责人映射存储文件（JSON）。
const ownersFilePath = "owners.json"
// 配置页 HTML 文件路径
const configHTMLPath = "config.html"

type ownerStore struct {
    mu   sync.RWMutex
    data map[string]string
}

func newOwnerStore() *ownerStore {
    return &ownerStore{data: map[string]string{}}
}

func (s *ownerStore) Get(id string) string {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.data[id]
}

func (s *ownerStore) Upsert(id, owner string) {
    s.mu.Lock()
    s.data[id] = owner
    s.mu.Unlock()
}

func (s *ownerStore) Delete(id string) {
    s.mu.Lock()
    delete(s.data, id)
    s.mu.Unlock()
}

func (s *ownerStore) All() map[string]string {
    s.mu.RLock()
    defer s.mu.RUnlock()
    out := make(map[string]string, len(s.data))
    for k, v := range s.data {
        out[k] = v
    }
    return out
}

func (s *ownerStore) LoadFromFile(path string) error {
    f, err := os.Open(path)
    if err != nil {
        if os.IsNotExist(err) {
            return nil
        }
        return err
    }
    defer f.Close()
    b, err := io.ReadAll(f)
    if err != nil {
        return err
    }
    if len(b) == 0 {
        return nil
    }
    var m map[string]string
    if err := json.Unmarshal(b, &m); err != nil {
        return err
    }
    s.mu.Lock()
    s.data = m
    s.mu.Unlock()
    return nil
}

func (s *ownerStore) SaveToFile(path string) error {
    s.mu.RLock()
    b, err := json.MarshalIndent(s.data, "", "  ")
    s.mu.RUnlock()
    if err != nil {
        return err
    }
    return os.WriteFile(path, b, 0644)
}

var owners = newOwnerStore()

type pushPlusRequest struct {
    Token    string `json:"token"`
    Title    string `json:"title,omitempty"`
    Content  string `json:"content"`
    Topic    string `json:"topic,omitempty"`
    Template string `json:"template,omitempty"`
}

type pushPlusResponse struct {
    Code int    `json:"code"`
    Msg  string `json:"msg"`
    Data string `json:"data"`
}

func main() {
    mux := http.NewServeMux()
    mux.HandleFunc("/webhook", webhookHandler)
    mux.HandleFunc("/config", configPageHandler)
    mux.HandleFunc("/api/owners", ownersAPIHandler)

    addr := httpAddr

    if err := owners.LoadFromFile(ownersFilePath); err != nil {
        log.Printf("加载负责人映射失败: %v", err)
    } else {
        log.Printf("负责人映射已加载，条目数: %d", len(owners.All()))
    }

    log.Printf("HTTP 服务启动，监听 %s，POST %s 接收 JSON", addr, "/webhook")
    if err := http.ListenAndServe(addr, mux); err != nil {
        log.Fatalf("HTTP 服务启动失败: %v", err)
    }
}

func sendPushPlus(req pushPlusRequest) error {
    body, err := json.Marshal(req)
    if err != nil {
        return fmt.Errorf("编码请求失败: %w", err)
    }

    httpClient := &http.Client{Timeout: 10 * time.Second}
    httpReq, err := http.NewRequest(http.MethodPost, "https://www.pushplus.plus/send", bytes.NewReader(body))
    if err != nil {
        return fmt.Errorf("创建 HTTP 请求失败: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := httpClient.Do(httpReq)
    if err != nil {
        return fmt.Errorf("发送失败: %w", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("HTTP 状态码异常: %d", resp.StatusCode)
    }

    var result pushPlusResponse
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return fmt.Errorf("解析响应失败: %w", err)
    }

    if result.Code != 200 {
        return fmt.Errorf("推送失败，code=%d msg=%s", result.Code, result.Msg)
    }

    log.Printf("请求成功，任务 ID: %s", result.Data)
    return nil
}

// webhookHandler 接收 JSON 并转发到 PushPlus
func webhookHandler(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    // 先接收任意 JSON 结构
    var raw map[string]any
    if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
        http.Error(w, fmt.Sprintf("invalid json: %v", err), http.StatusBadRequest)
        return
    }

    // 在这里进行“手动处理”：将原始 JSON 转换为 PushPlus 请求
    // 在转换前，尝试从负责人映射中查找机器ID对应的负责人，并注入 raw["owner"]
    if mid := extractMachineID(raw); mid != "" {
        if owner := owners.Get(mid); owner != "" {
            raw["owner"] = owner
        }
    }
    in, err := transformIncomingToPushPlus(raw)
    if err != nil {
        http.Error(w, fmt.Sprintf("transform error: %v", err), http.StatusBadRequest)
        return
    }

    // 强制使用代码里硬编码的 token；无需再从请求或环境变量读取
    in.Token = pushPlusTokenHardcoded
    // 强制使用代码里硬编码的 topic（若配置了）
    in.Topic = pushPlusTopicHardcoded

    if in.Token == "" {
        http.Error(w, "missing token (未配置硬编码 token)", http.StatusBadRequest)
        return
    }

    if in.Content == "" {
        http.Error(w, "missing content", http.StatusBadRequest)
        return
    }

    if in.Template == "" {
        in.Template = "markdown"
    }

    in.Content = fmt.Sprintf("【警告】%s", in.Content)


    if err := sendPushPlus(in); err != nil {
        log.Printf("推送失败: %v", err)
        http.Error(w, fmt.Sprintf("push failed: %v", err), http.StatusBadGateway)
        return
    }

    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    _ = json.NewEncoder(w).Encode(map[string]any{
        "ok":   true,
        "msg":  "sent",
        "time": time.Now().Format(time.RFC3339),
    })
}

// 简单的管理页，填写/查看/删除 机器ID -> 负责人姓名
func configPageHandler(w http.ResponseWriter, r *http.Request) {
    http.ServeFile(w, r, configHTMLPath)
}

// API: GET 列表；POST 新增/更新；DELETE 删除
func ownersAPIHandler(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    switch r.Method {
    case http.MethodGet:
        _ = json.NewEncoder(w).Encode(owners.All())
        return
    case http.MethodPost:
        var in struct {
            ID    string `json:"id"`
            Owner string `json:"owner"`
        }
        if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
            http.Error(w, fmt.Sprintf("invalid json: %v", err), http.StatusBadRequest)
            return
        }
        if in.ID == "" || in.Owner == "" {
            http.Error(w, "id/owner 均不能为空", http.StatusBadRequest)
            return
        }
        owners.Upsert(in.ID, in.Owner)
        if err := owners.SaveToFile(ownersFilePath); err != nil {
            http.Error(w, fmt.Sprintf("save failed: %v", err), http.StatusInternalServerError)
            return
        }
        _ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
        return
    case http.MethodDelete:
        var in struct {
            ID string `json:"id"`
        }
        if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
            http.Error(w, fmt.Sprintf("invalid json: %v", err), http.StatusBadRequest)
            return
        }
        if in.ID == "" {
            http.Error(w, "id 不能为空", http.StatusBadRequest)
            return
        }
        owners.Delete(in.ID)
        if err := owners.SaveToFile(ownersFilePath); err != nil {
            http.Error(w, fmt.Sprintf("save failed: %v", err), http.StatusInternalServerError)
            return
        }
        _ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
        return
    default:
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
}



// 将任意 JSON 转为 PushPlus 请求；可根据你的业务自由修改
func transformIncomingToPushPlus(raw map[string]any) (pushPlusRequest, error) {
    token := getFirstString(raw, "token")
    title := getFirstString(raw, "title", "subject")
    topic := getFirstString(raw, "topic", "group")
    template := getFirstString(raw, "template", "tpl")

    // content 根据你的需求自定义编辑（支持模板），默认按常见字段拼装
    content := renderContentFromRaw(raw)

    if title == "" {
        title = "告警通知"
    }

    return pushPlusRequest{
        Token:    token,
        Title:    title,
        Content:  content,
        Topic:    topic,
        Template: template,
    }, nil
}

// 优先使用硬编码模板渲染内容；
// 模板示例：
// {{.subject}} | {{.level}} | {{.service}} - {{.msg}}
// 嵌套字段使用：{{index .detail "host"}}
func renderContentFromRaw(raw map[string]any) string {
    if tpl := contentTemplateHardcoded; tpl != "" {
        t, err := template.New("content").Option("missingkey=zero").Parse(tpl)
        if err == nil {
            var buf bytes.Buffer
            if err := t.Execute(&buf, raw); err == nil {
                s := buf.String()
                if s != "" {
                    return s
                }
            }
        }
    }
    return buildDefaultContent(raw)
}

func buildDefaultContent(raw map[string]any) string {
    title := getFirstString(raw, "title", "subject")
    level := getFirstString(raw, "level", "severity", "status")
    service := getFirstString(raw, "service", "app", "project")
    host := getFirstString(raw, "host", "hostname")
    machineID := getFirstString(raw, "machine_id", "id", "instance", "server_id", "host", "hostname")
    ip := getFirstString(raw, "ip", "ipaddr", "remote_ip")
    code := getFirstString(raw, "code", "status_code")
    ts := getFirstString(raw, "timestamp", "ts", "time", "datetime")
    msg := getFirstString(raw, "content", "message", "msg", "text", "error")
    owner := getFirstString(raw, "owner")

    var buf bytes.Buffer
    buf.WriteString("### ")
    if title != "" {
        buf.WriteString(title)
    } else {
        buf.WriteString("告警通知")
    }
    buf.WriteString("\n")

    writeKVLine := func(k, v string) {
        if v != "" {
            buf.WriteString("- ")
            buf.WriteString(k)
            buf.WriteString(": ")
            buf.WriteString(v)
            buf.WriteString("\n")
        }
    }

    writeKVLine("级别", level)
    writeKVLine("服务", service)
    writeKVLine("主机", host)
    writeKVLine("机器ID", machineID)
    writeKVLine("IP", ip)
    writeKVLine("状态码", code)
    writeKVLine("时间", ts)
    writeKVLine("负责人", owner)

    if msg != "" {
        buf.WriteString("\n")
        buf.WriteString(msg)
        buf.WriteString("\n")
    } else {
        buf.WriteString("\n")
        buf.WriteString(buildContentFromJSON(raw, "token"))
        buf.WriteString("\n")
    }

    return buf.String()
}

func extractMachineID(raw map[string]any) string {
    return getFirstString(raw, "machine_id", "id", "instance", "server_id", "host", "hostname")
}

func getFirstString(m map[string]any, keys ...string) string {
    for _, k := range keys {
        if v, ok := m[k]; ok {
            if s, ok := v.(string); ok {
                return s
            }
        }
    }
    return ""
}

func buildContentFromJSON(m map[string]any, removeKeys ...string) string {
    // 复制一份，去掉敏感字段
    filtered := map[string]any{}
    for k, v := range m {
        filtered[k] = v
    }
    for _, k := range removeKeys {
        delete(filtered, k)
    }

    b, err := json.MarshalIndent(filtered, "", "  ")
    if err != nil {
        return "收到无法序列化的 JSON 数据"
    }
    return "```json\n" + string(b) + "\n```"
}


package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "time"
)

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
    payload := pushPlusRequest{
        Token:    "257c7c06da1047eea5c5f5a250487d46",
        Title:    "告警通知",
        Content:  "测试消息，请忽略，使用自定义模版发送",
        Topic:    "20251105",     // 群组编码，可选
        Template: "markdown",      // 可选模板：html、json、markdown、txt
    }

    if err := sendPushPlus(payload); err != nil {
        log.Fatalf("推送失败: %v", err)
    }

    log.Println("推送成功")
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


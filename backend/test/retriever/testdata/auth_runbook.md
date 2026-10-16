# AuroraAuth 认证故障处理

AuroraAuth 服务在签名密钥轮转后出现 HTTP 401 时，先检查 signing-key-cache 是否缓存了旧密钥。当前告警是密钥轮转后的令牌签名校验失败。

## 恢复认证

恢复 AuroraAuth 认证的步骤：刷新 signing-key-cache 缓存，重新加载最新签名密钥，再重试失败的令牌认证请求。不要直接关闭签名校验。

## 验证恢复

验证 AuroraAuth 认证恢复：检查 /healthz 返回正常，使用新签发令牌调用 /api/session，确认 HTTP 200 且 401 错误率回落。

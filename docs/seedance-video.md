# Seedance 视频中转

此分支在同一个 New API 进程内提供盈合视频渠道、网页视频生成页和远程 MCP，不需要额外运行 Node.js 或 Python 服务，也不会下载、转码或代理视频文件。

## 渠道配置

在管理后台新增渠道：

- 类型：`FZYinghe Video`
- Base URL：`https://api-aigc.fzyinghe.com`
- 密钥：盈合平台 API Key
- 模型：
  - `cheap-seedance-2.0`
  - `cheap-seedance-2.0-fast`
  - `cheap-seedance-2.0-mini`

不要把上游 API Key 写入仓库、镜像或 MCP 客户端。用户在 MCP 客户端中使用的是各自的 New API 令牌。

## 计费

默认模型价格已经包含 20% 加价，并以 720p 每秒为基准：

| 模型 | 480p | 720p | 1080p | 4K |
| --- | ---: | ---: | ---: | ---: |
| cheap-seedance-2.0 | ¥0.3600/秒 | ¥0.7200/秒 | ¥1.8000/秒 | ¥3.6000/秒 |
| cheap-seedance-2.0-fast | ¥0.2880/秒 | ¥0.5760/秒 | — | — |
| cheap-seedance-2.0-mini | ¥0.1800/秒 | ¥0.3600/秒 | — | — |

实际扣费还会乘以用户分组倍率。若要保持表中金额，请确保充值换算、充值分组倍率和调用分组倍率都为 `1`。

网页和 MCP 使用渠道中配置的对外模型名；如果渠道设置了模型映射，调用时应传映射前的渠道模型名，不要把文档中的 `cheap-*` 示例名硬编码到客户端。

## 网页使用

登录后打开 `/video`。页面支持公网 HTTP/HTTPS 素材地址，也支持选择 JPG、PNG、WEBP 本地图片。文件由浏览器直传临时 OSS，New API 不代理文件字节；未配置临时 OSS 时仍可只填写公网 URL。

任务成功后，页面直接使用上游签名视频地址播放。该地址默认 24 小时过期，应及时保存。

## Codex

把用户自己的 New API 令牌写入本地环境变量，并用 Codex CLI 注册远程 MCP：

```bash
export METEOR_VIDEO_TOKEN='sk-用户自己的令牌'
codex mcp add meteor-video \
  --url https://api.meteor21c.fun/mcp \
  --bearer-token-env-var METEOR_VIDEO_TOKEN
codex mcp list
```

也可以手动在 `~/.codex/config.toml` 中加入：

```toml
[mcp_servers.meteor_video]
url = "https://api.meteor21c.fun/mcp"
bearer_token_env_var = "METEOR_VIDEO_TOKEN"
tool_timeout_sec = 60
```

重启 Codex 后会出现 `create_video`、`get_video`、`create_material_upload` 和 `create_image` 工具。

## Claude Code

```bash
export METEOR_VIDEO_TOKEN='sk-用户自己的令牌'
claude mcp add --transport http meteor-video \
  https://api.meteor21c.fun/mcp \
  --header "Authorization: Bearer ${METEOR_VIDEO_TOKEN}"
```

也可以在 `.mcp.json` 中通过环境变量配置：

```json
{
  "mcpServers": {
    "meteor-video": {
      "type": "http",
      "url": "https://api.meteor21c.fun/mcp",
      "headers": {
        "Authorization": "Bearer ${METEOR_VIDEO_TOKEN}"
      }
    }
  }
}
```

## 异步工作流

1. 调用 `create_video`，保存返回的 `task_id`。
2. 每隔数秒调用 `get_video`。
3. `status` 为 `SUCCESS` 时读取 `data.result_url`。
4. `status` 为 `FAILURE` 时读取 `data.fail_reason`。

## 镜像

推送 `feature/seedance-video` 分支后，`Build Seedance image` 工作流会先检查后端和前端，再发布多架构镜像：

```text
ghcr.io/meteor21c/new-api-seedance:seedance
```

生产部署只需将现有 Compose 中 New API 服务的 `image` 改为该镜像；MySQL、Redis、数据目录、日志目录和环境变量保持不变。

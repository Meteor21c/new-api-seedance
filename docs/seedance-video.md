# Seedance / Kling 视频中转

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
  - `doubao-seedance-2.0`
  - `doubao-seedance-2.0-fast`
  - `doubao-seedance-2.0-mini`
  - `doubao-seedance-2.5`
  - `kling-v3`
  - `kling-v3-omni`

不要把上游 API Key 写入仓库、镜像或 MCP 客户端。用户在 MCP 客户端中使用的是各自的 New API 令牌。

## 计费

该渠道同时支持两套互不混用的计费逻辑：

- `cheap-seedance-*` 和 Kling 继续按次预扣，并根据时长、分辨率等参数折算成“元/秒”。
- `doubao-seedance-*` 根据上游任务完成时返回的 `tokenUsage.totalTokens` 差额结算。Token 已经包含生成时长和输出规模，因此不会再乘一次时长或旧分辨率倍率。

### 旧模型：按秒计费

默认模型价格已经包含 20% 加价，并以 720p 每秒为基准：

| 模型 | 480p | 720p | 1080p | 4K |
| --- | ---: | ---: | ---: | ---: |
| cheap-seedance-2.0 | ¥0.3600/秒 | ¥0.7200/秒 | ¥1.8000/秒 | ¥3.6000/秒 |
| cheap-seedance-2.0-fast | ¥0.2880/秒 | ¥0.5760/秒 | — | — |
| cheap-seedance-2.0-mini | ¥0.1800/秒 | ¥0.3600/秒 | — | — |

Kling V3 价格还会根据音频和参考视频动态变化。下表为成本价加 20% 后的默认零售价（充值比例和分组倍率均为 1 时）：

| 模型 | 条件 | 720p | 1080p | 4K |
| --- | --- | ---: | ---: | ---: |
| kling-v3 | 无声 | ¥0.4680/秒 | ¥0.6240/秒 | ¥2.3400/秒 |
| kling-v3 | 有声 | ¥0.8580/秒 | ¥1.0920/秒 | ¥2.3400/秒 |
| kling-v3-omni | 无参考视频、无声 | ¥0.4680/秒 | ¥0.6240/秒 | ¥2.3400/秒 |
| kling-v3-omni | 无参考视频、有声 | ¥0.6240/秒 | ¥0.7800/秒 | ¥2.3400/秒 |
| kling-v3-omni | 有参考视频、无声 | ¥0.7020/秒 | ¥0.9360/秒 | ¥2.3400/秒 |
| kling-v3-omni | 有参考视频、有声 | ¥0.8580/秒 | ¥1.0920/秒 | ¥2.3400/秒 |

Kling 的请求会转换为文档规定的字段：`kling-v3` 使用 `model_name`、`mode`、`sound`、`image`/`image_tail`；`kling-v3-omni` 使用 `image_list` 和 `video_list`。渠道模型映射仍然生效，前台显示和 MCP 应使用渠道中配置的模型名。

实际扣费还会乘以用户分组倍率。若要保持表中金额，请确保充值换算、充值分组倍率和调用分组倍率都为 `1`。

### 新模型：按 Token 计费

在“系统设置 → 模型设置 → 模型倍率”的可视化编辑器中，逐个新增模型并这样设置：

1. 模型名填写完整的 `doubao-seedance-*` 名称。
2. 计费方式选择“按 Token / Per-token”，不要选择“按次 / Per-request”。
3. 只填写“输入价格 / Input price（每 1M Tokens）”；不要为这些视频模型设置固定模型价格，也不要启用输出价格等从属价格。
4. 当前站点充值按数值 `1:1` 处理。即使界面标签仍写着 USD，也直接填写人民币数值，不要除以 `7.3`。
5. 调用分组倍率保持 `1` 时，填写的数值就是该模型“无输入视频、基础分辨率”的销售单价。

上游原价与场景如下；程序会自动匹配是否含输入视频和输出分辨率：

| 模型 | 场景 | 480p | 720p | 1080p | 4K |
| --- | --- | ---: | ---: | ---: | ---: |
| doubao-seedance-2.0 | 无输入视频 | ¥46/1M | ¥46/1M | ¥51/1M | ¥26/1M |
| doubao-seedance-2.0 | 含输入视频 | ¥28/1M | ¥28/1M | ¥31/1M | ¥16/1M |
| doubao-seedance-2.0-fast | 无输入视频 | ¥37/1M | ¥37/1M | — | — |
| doubao-seedance-2.0-fast | 含输入视频 | ¥22/1M | ¥22/1M | — | — |
| doubao-seedance-2.0-mini | 无输入视频 | ¥23/1M | ¥23/1M | — | — |
| doubao-seedance-2.0-mini | 含输入视频 | ¥14/1M | ¥14/1M | — | — |
| doubao-seedance-2.5 | 无输入视频 | ¥70/1M | ¥70/1M | — | — |
| doubao-seedance-2.5 | 含输入视频 | ¥42/1M | ¥42/1M | — | — |

收到上游折扣和目标加价率后，四个模型的“输入价格”分别填写：

```text
2.0      = 46 × 上游折扣 × (1 + 加价率)
2.0-fast = 37 × 上游折扣 × (1 + 加价率)
2.0-mini = 23 × 上游折扣 × (1 + 加价率)
2.5      = 70 × 上游折扣 × (1 + 加价率)
```

这里的折扣和加价率使用小数，例如八折为 `0.8`、加价 20% 为 `0.2`。同一模型其余场景按上表比例自动换算。最终公式为：

```text
实际金额 = totalTokens ÷ 1,000,000 × 当前场景每百万 Token 单价 × 调用分组倍率
```

网页在选择新模型时不显示一个不可靠的预估总价，只提示按 Token 计费、以实际返回为准；任务完成后会显示上游 `totalTokens` 和最终扣费金额。

网页和 MCP 使用渠道中配置的对外模型名；如果渠道设置了模型映射，调用时应传映射前的渠道模型名，不要把文档中的 `cheap-*` 示例名硬编码到客户端。

## MCP 入口

定制版在同一个 New API 进程中提供三个远程 MCP 入口，不需要额外部署服务：

| 入口 | 客户端名称 | 令牌分组 | 可用工具 |
| --- | --- | --- | --- |
| `/mcp/image` | `meteor-image` | 绘图专用分组 | `create_image` |
| `/mcp/video` | `meteor-video` | 视频专用分组 | `create_video`、`get_video`、`create_material_upload` |
| `/mcp` | 旧版兼容入口 | 取决于令牌分组 | 全部媒体工具 |

网页图片和视频生成页继续使用原有网页接口，不会改走 MCP。图片 MCP 会要求上游返回 `b64_json`，但会在服务端把结果转换为 MCP 标准的原生图片内容；Codex 和 Claude 可以直接显示图片，不依赖短时效的上游图片地址，也不需要客户编写 Base64 解码脚本。

## 网页使用

登录后打开 `/video`。页面支持公网 HTTP/HTTPS 素材地址，也支持选择 JPG、PNG、WEBP 本地图片。文件由浏览器直传临时 OSS，New API 不代理文件字节；未配置临时 OSS 时仍可只填写公网 URL。

任务成功后，New API 会返回与当前用户和任务绑定的 24 小时签名内容地址，页面
直接用该地址播放。上游地址和 New API 用户令牌都不会暴露在链接中；客户也可把
同一地址交给浏览器、播放器、Codex、Claude 或下载器打开。最近任务和签名地址
均按 24 小时设计，应在期限内打开或保存成品。

## Codex

分别创建“绘图专用分组”和“视频专用分组”的 New API 用户令牌，再用 Codex CLI 注册两个远程 MCP。不要使用上游渠道密钥：

```bash
export METEOR_IMAGE_TOKEN='sk-绘图专用分组令牌'
export METEOR_VIDEO_TOKEN='sk-用户自己的令牌'

codex mcp remove meteor-image >/dev/null 2>&1 || true
codex mcp remove meteor-video >/dev/null 2>&1 || true

codex mcp add meteor-image \
  --url https://api.meteor21c.fun/mcp/image \
  --bearer-token-env-var METEOR_IMAGE_TOKEN
codex mcp add meteor-video \
  --url https://api.meteor21c.fun/mcp/video \
  --bearer-token-env-var METEOR_VIDEO_TOKEN
codex mcp list
```

也可以手动在 `~/.codex/config.toml` 中加入：

```toml
[mcp_servers.meteor_image]
url = "https://api.meteor21c.fun/mcp/image"
bearer_token_env_var = "METEOR_IMAGE_TOKEN"
tool_timeout_sec = 300

[mcp_servers.meteor_video]
url = "https://api.meteor21c.fun/mcp/video"
bearer_token_env_var = "METEOR_VIDEO_TOKEN"
tool_timeout_sec = 60
```

重启 Codex 后，`meteor-image` 只会出现 `create_image`，`meteor-video` 只会出现 `create_video`、`get_video` 和 `create_material_upload`。`create_image` 会直接返回 MCP 原生图片，客户端可以展示，并可按客户端能力保存或导出到本地；不需要 `OPENAI_API_KEY`，也不需要直接调用 `/v1/images/generations`。

## Claude Code

```bash
export METEOR_IMAGE_TOKEN='sk-绘图专用分组令牌'
export METEOR_VIDEO_TOKEN='sk-用户自己的令牌'
claude mcp add --transport http meteor-image \
  https://api.meteor21c.fun/mcp/image \
  --header "Authorization: Bearer ${METEOR_IMAGE_TOKEN}"
claude mcp add --transport http meteor-video \
  https://api.meteor21c.fun/mcp/video \
  --header "Authorization: Bearer ${METEOR_VIDEO_TOKEN}"
```

也可以在 `.mcp.json` 中通过环境变量配置：

```json
{
  "mcpServers": {
    "meteor-image": {
      "type": "http",
      "url": "https://api.meteor21c.fun/mcp/image",
      "headers": {
        "Authorization": "Bearer ${METEOR_IMAGE_TOKEN}"
      }
    },
    "meteor-video": {
      "type": "http",
      "url": "https://api.meteor21c.fun/mcp/video",
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
3. `status` 为 `SUCCESS` 时读取 `data.result_url`。该地址是与用户和任务绑定的
   24 小时签名链接，可直接交给浏览器、Codex、Claude 或下载器打开，无需把
   New API 用户令牌拼进地址；签名过期或被篡改时会自动失效。
4. `status` 为 `FAILURE` 时读取 `data.fail_reason`。

## 镜像

推送 `feature/seedance-video` 分支后，`Build Seedance image` 工作流会先检查后端和前端，再发布多架构镜像：

```text
ghcr.io/meteor21c/new-api-seedance:seedance
```

生产部署只需将现有 Compose 中 New API 服务的 `image` 改为该镜像；MySQL、Redis、数据目录、日志目录和环境变量保持不变。

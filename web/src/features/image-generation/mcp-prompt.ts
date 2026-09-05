/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

export type ImageMcpPromptValues = {
  model: string
  prompt: string
  n: number
  size: string
  quality: string
}

export function imageMcpEndpoint(origin?: string): string {
  return origin
    ? `${origin.replace(/\/$/, '')}/mcp/image`
    : 'https://api.meteor21c.fun/mcp/image'
}

export function buildImageMcpPrompt(
  values: ImageMcpPromptValues,
  referenceFiles: File[],
  endpoint: string
): string {
  const references =
    referenceFiles.length > 0
      ? referenceFiles.map((file) => `本地文件：${file.name}`).join('、')
      : '无'

  return [
    '【图片生成任务】',
    '',
    '请使用已配置的 meteor-image MCP 生成图片。',
    '',
    `模型：${values.model.trim() || '未填写'}`,
    `MCP 服务地址：${endpoint}`,
    '令牌：请将创建的【绘图专用分组】下的 API Key 填入这里：YOUR_NEW_API_TOKEN',
    '提示词：',
    values.prompt.trim(),
    '',
    '参数：',
    `- 图片数量：${values.n}`,
    `- 尺寸：${values.size}`,
    `- 质量：${values.quality}`,
    `参考图片：${references}`,
    '',
    '-------------------------------------------------------------------',
    '',
    '【AI 执行须知】',
    '',
    '你是 MCP 图片生成执行器。',
    '',
    '1. 仅使用当前客户端的 meteor-image MCP；不得改用 meteor-video，也不得直接调用第三方上游 API。',
    '2. 若 meteor-image 尚未配置或鉴权失败，用上述服务地址和令牌完成配置后再继续；令牌只在当前客户端配置中使用，不得写入项目代码、日志或最终回复。',
    '3. 先确认 create_image 工具可用。',
    '4. 如果提示词为空，只向用户索要提示词，不得猜测或提交。',
    '5. 严格使用用户填写的模型和参数；auto 表示不传对应可选参数。',
    '6. 调用 create_image 后，从 data[].b64_json 解码图片并保存为本地文件，再把文件返回给用户；不要要求 OPENAI_API_KEY。',
    '7. 当前 meteor-image MCP 只支持文生图。若列出了本地参考图片，应明确告知网页参考图不会随复制文本上传，并先征得用户同意后再按纯文本提示词生成。',
    '8. 在当前图片请求返回成功或失败前，不要提交新的图片请求。',
    '',
    '注意：复制内容不包含本地文件字节。如上面显示本地文件，请在 Codex/Claude 中重新附加同名文件；当前 MCP 不会把该文件作为参考图提交。',
  ].join('\n')
}

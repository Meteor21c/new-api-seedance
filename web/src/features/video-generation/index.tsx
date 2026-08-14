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

import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Copy,
  Clock3,
  Download,
  ExternalLink,
  Film,
  ImagePlus,
  LoaderCircle,
  Volume2,
  X,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Main } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardAction,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Progress } from '@/components/ui/progress'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import {
  readGenerationHistory,
  readGenerationRecord,
  writeGenerationHistory,
  writeGenerationRecord,
} from '../generation-storage'
import {
  createVideoTracked,
  getVideoContent,
  getVideoModels,
  getVideoTask,
  getVideoTasks,
  uploadMaterial,
} from './api'
import { VideoPriceComparison } from './price-comparison'
import {
  VIDEO_ASPECT_RATIOS,
  VIDEO_MODES,
  type VideoBillingMode,
  type VideoGenerationRequest,
  type VideoAspectRatio,
  type VideoModelKind,
  type VideoResolution,
  type VideoTask,
  type VideoTier,
} from './types'

function inferredModelKind(modelName: string): VideoModelKind {
  const normalized = modelName.trim().toLowerCase()
  if (normalized === 'kling-v3') return 'kling-v3'
  if (normalized === 'kling-v3-omni') return 'kling-v3-omni'
  if (normalized.startsWith('grok-imagine-video')) return 'grok-video'
  if (normalized.includes('seedance')) return 'seedance'
  return 'unknown'
}

function modelKindForSelection(
  modelName: string,
  metadataKind?: VideoModelKind
): VideoModelKind {
  // Prefer the exact channel model name when it identifies a built-in model.
  // This keeps the UI correct even when an old cached model-list response has
  // no `kind` field. Custom channel aliases still use the server-provided
  // metadata.
  const inferred = inferredModelKind(modelName)
  return inferred === 'unknown' ? (metadataKind ?? 'unknown') : inferred
}

function inferredModelTier(modelName: string): VideoTier {
  const normalized = modelName.trim().toLowerCase()
  if (normalized.endsWith('-mini')) return 'mini'
  if (normalized.endsWith('-fast')) return 'fast'
  return 'standard'
}

function minimumDurationForKind(kind: VideoModelKind): number {
  return kind === 'kling-v3' ||
    kind === 'kling-v3-omni' ||
    kind === 'grok-video'
    ? 3
    : 4
}

function isKlingKind(kind: VideoModelKind): boolean {
  return kind === 'kling-v3' || kind === 'kling-v3-omni'
}

function inferredBillingMode(modelName: string): VideoBillingMode {
  const normalized = modelName.trim().toLowerCase()
  return [
    'doubao-seedance-2.0',
    'doubao-seedance-2.0-fast',
    'doubao-seedance-2.0-mini',
    'doubao-seedance-2.5',
  ].includes(normalized)
    ? 'per-token'
    : 'per-second'
}

const KLING_ASPECT_RATIOS = ['16:9', '9:16', '1:1'] as const
const GROK_ASPECT_RATIOS = ['16:9', '9:16', '1:1', '4:3', '3:4'] as const
const MAX_VIDEO_REFERENCE_IMAGES = 4
const MAX_VIDEO_REFERENCE_IMAGE_SIZE = 10 * 1024 * 1024
const VIDEO_REFERENCE_IMAGE_TYPES = new Set([
  'image/jpeg',
  'image/png',
  'image/webp',
])

function isSupportedVideoReferenceImage(file: File): boolean {
  return VIDEO_REFERENCE_IMAGE_TYPES.has(file.type)
}

function videoReferenceFileKey(file: File): string {
  return `${file.name}-${file.size}-${file.lastModified}-${file.type}`
}

function aspectRatiosForKind(kind: VideoModelKind): VideoAspectRatio[] {
  if (kind === 'kling-v3' || kind === 'kling-v3-omni') {
    return [...KLING_ASPECT_RATIOS]
  }
  if (kind === 'grok-video') return [...GROK_ASPECT_RATIOS]
  return [...VIDEO_ASPECT_RATIOS]
}

const videoFormSchema = z
  .object({
    model: z.string().trim().min(1, 'Model is required'),
    prompt: z
      .string()
      .trim()
      .min(1, 'Prompt is required')
      .max(1300, 'Prompt must not exceed 1300 characters'),
    duration: z
      .number()
      .int()
      .min(3, 'Duration must be at least 3 seconds')
      .max(15, 'Duration must not exceed 15 seconds'),
    resolution: z.enum(['480p', '720p', '1080p', '4K']),
    aspectRatio: z.enum(VIDEO_ASPECT_RATIOS),
    mode: z.enum(VIDEO_MODES),
    audio: z.boolean(),
    referenceUrls: z.string(),
    startImageUrl: z.string(),
    endImageUrl: z.string(),
  })
  .superRefine((values, context) => {
    const kind = inferredModelKind(values.model)
    const tier = inferredModelTier(values.model)
    const minimum = minimumDurationForKind(kind)
    const allowedResolutions = resolutionsForModel(kind, tier)
    if (values.duration < minimum) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['duration'],
        message: `Duration must be at least ${minimum} seconds`,
      })
    }
    if (!allowedResolutions.includes(values.resolution)) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['resolution'],
        message: `Resolution ${values.resolution} is not supported by the selected model`,
      })
    }
    if (
      (kind === 'kling-v3' || kind === 'kling-v3-omni') &&
      !KLING_ASPECT_RATIOS.includes(
        values.aspectRatio as (typeof KLING_ASPECT_RATIOS)[number]
      )
    ) {
      context.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['aspectRatio'],
        message: 'Kling supports only 16:9, 9:16, or 1:1',
      })
    }
  })

type VideoFormValues = z.infer<typeof videoFormSchema>

const PRICE_PER_SECOND: Record<
  VideoTier,
  Partial<Record<VideoResolution, number>>
> = {
  standard: {
    '480p': 0.36,
    '720p': 0.72,
    '1080p': 1.8,
    '4K': 3.6,
  },
  fast: {
    '480p': 0.288,
    '720p': 0.576,
  },
  mini: {
    '480p': 0.18,
    '720p': 0.36,
  },
}

const KLING_V3_PRICES = {
  silent: { '720p': 0.468, '1080p': 0.624, '4K': 2.34 },
  audio: { '720p': 0.858, '1080p': 1.092, '4K': 2.34 },
} satisfies Record<string, Record<'720p' | '1080p' | '4K', number>>

const KLING_V3_OMNI_PRICES = {
  noReferenceVideo: {
    silent: { '720p': 0.468, '1080p': 0.624, '4K': 2.34 },
    audio: { '720p': 0.624, '1080p': 0.78, '4K': 2.34 },
  },
  referenceVideo: {
    silent: { '720p': 0.702, '1080p': 0.936, '4K': 2.34 },
    audio: { '720p': 0.858, '1080p': 1.092, '4K': 2.34 },
  },
} satisfies Record<
  string,
  Record<string, Record<'720p' | '1080p' | '4K', number>>
>

const TERMINAL_STATUSES = new Set(['SUCCESS', 'FAILURE'])

function resolutionsForModel(
  kind: VideoModelKind,
  tier: VideoTier,
  configuredResolutions?: VideoResolution[]
): VideoResolution[] {
  if (configuredResolutions?.length) return configuredResolutions
  if (kind === 'kling-v3' || kind === 'kling-v3-omni') {
    return ['720p', '1080p', '4K']
  }
  if (kind === 'grok-video') return ['480p', '720p']
  return tier === 'standard'
    ? ['480p', '720p', '1080p', '4K']
    : ['480p', '720p']
}

function hasReferenceVideo(raw: string): boolean {
  return splitReferenceUrls(raw).some((value) => {
    const url = value.replace(/^(reference|start|end):/i, '')
    return /\.mp4(?:$|[?#])/i.test(url)
  })
}

function estimateKlingPrice(
  kind: VideoModelKind,
  resolution: VideoResolution,
  audio: boolean,
  referenceUrls: string
): number {
  if (kind === 'kling-v3') {
    return (
      (audio ? KLING_V3_PRICES.audio : KLING_V3_PRICES.silent)[
        resolution as keyof typeof KLING_V3_PRICES.silent
      ] ?? 0
    )
  }
  if (kind === 'kling-v3-omni') {
    const table = hasReferenceVideo(referenceUrls)
      ? KLING_V3_OMNI_PRICES.referenceVideo
      : KLING_V3_OMNI_PRICES.noReferenceVideo
    return (
      (audio ? table.audio : table.silent)[
        resolution as keyof typeof table.silent
      ] ?? 0
    )
  }
  return 0
}

function estimateVideoPricePerSecond(
  kind: VideoModelKind,
  tier: VideoTier,
  resolution: VideoResolution,
  audio: boolean,
  referenceUrls: string,
  configuredPrice?: number
): number {
  if (Number.isFinite(configuredPrice)) {
    if (kind === 'grok-video') return configuredPrice ?? 0
    if (isKlingKind(kind)) {
      const scenarioPrice = estimateKlingPrice(
        kind,
        resolution,
        audio,
        referenceUrls
      )
      const baselinePrice = estimateKlingPrice(kind, '720p', false, '')
      return baselinePrice > 0
        ? (configuredPrice ?? 0) * (scenarioPrice / baselinePrice)
        : (configuredPrice ?? 0)
    }
    const scenarioPrice = PRICE_PER_SECOND[tier][resolution] ?? 0
    const baselinePrice = PRICE_PER_SECOND[tier]['720p'] ?? 0
    return baselinePrice > 0
      ? (configuredPrice ?? 0) * (scenarioPrice / baselinePrice)
      : (configuredPrice ?? 0)
  }
  if (isKlingKind(kind)) {
    return estimateKlingPrice(kind, resolution, audio, referenceUrls)
  }
  if (kind === 'grok-video') return 0.2
  return PRICE_PER_SECOND[tier][resolution] ?? 0
}

function audioDescriptionForKind(kind: VideoModelKind): string {
  if (kind === 'grok-video') {
    return 'Grok video includes audio by default; the toggle is not configurable'
  }
  if (isKlingKind(kind)) return 'Audio changes the listed price'
  return 'Audio does not change the listed price'
}

function progressValue(progress: string): number {
  const value = Number.parseInt(progress.replace('%', ''), 10)
  return Number.isFinite(value) ? value : 0
}

function statusVariant(status: string) {
  if (status === 'SUCCESS') return 'default' as const
  if (status === 'FAILURE') return 'destructive' as const
  return 'secondary' as const
}

function errorMessage(error: unknown): string {
  const responseData = (
    error as {
      response?: {
        data?: {
          message?: string
          error?: { message?: string }
        }
      }
    }
  )?.response?.data
  return (
    responseData?.error?.message ||
    responseData?.message ||
    (error instanceof Error ? error.message : '')
  )
}

function upstreamFailureHint(reason: string): string {
  if (reason.includes('当前用户未分配该模型可用的厂商')) {
    return 'The upstream API account has not been assigned a provider for this model. Enable the model in the upstream FZYinghe account or contact its support.'
  }
  if (reason.includes('未识别到 Generate 按钮实际积分')) {
    return 'The upstream provider could not detect the model price on its Generate page. This is an upstream provider availability or pricing-adapter issue, not the local New API price.'
  }
  return ''
}

function taskTimestamp(task: VideoTask): string {
  const timestamp = task.submit_time || task.created_at
  return new Date(timestamp * 1000).toLocaleString()
}

function taskSortTimestamp(task: VideoTask): number {
  return task.updated_at || task.created_at || task.submit_time || 0
}

function taskHistoryKey(task: VideoTask): string {
  return task.task_id || String(task.id)
}

function mergeVideoTasks(...lists: VideoTask[][]): VideoTask[] {
  const tasks = new Map<string, VideoTask>()
  for (const list of lists) {
    for (const task of list) {
      const key = taskHistoryKey(task)
      if (!key) continue
      const existing = tasks.get(key)
      if (!existing) {
        tasks.set(key, task)
        continue
      }
      const newer =
        taskSortTimestamp(task) >= taskSortTimestamp(existing) ? task : existing
      const older = newer === task ? existing : task
      tasks.set(key, { ...older, ...newer })
    }
  }
  return [...tasks.values()]
    .sort((left, right) => taskSortTimestamp(right) - taskSortTimestamp(left))
    .slice(0, 12)
}

function splitReferenceUrls(raw: string): string[] {
  return raw
    .split('\n')
    .map((value) => value.trim())
    .filter(Boolean)
}

function videoFileName(task: VideoTask, contentType = ''): string {
  let extension = 'mp4'
  if (contentType.includes('webm')) extension = 'webm'
  if (contentType.includes('quicktime')) extension = 'mov'
  const taskId = task.task_id.replaceAll(/[^a-zA-Z0-9_-]/g, '') || 'generated'
  return `video-${taskId}.${extension}`
}

function downloadVideoBlob(blob: Blob, fileName: string) {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = fileName
  anchor.rel = 'noopener'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  window.setTimeout(() => URL.revokeObjectURL(url), 1_000)
}

function mcpEndpoint(): string {
  if (typeof window !== 'undefined' && window.location.origin) {
    return `${window.location.origin}/mcp/video`
  }
  return 'https://api.meteor21c.fun/mcp/video'
}

function buildMcpPrompt(
  values: VideoFormValues,
  referenceFiles: File[],
  startFrameFile: File | null,
  endFrameFile: File | null
): string {
  const endpoint = mcpEndpoint()
  const referenceUrls = splitReferenceUrls(values.referenceUrls)
  const normalizedReferences = referenceUrls.map((value) =>
    value.replace(/^reference:/i, '').trim()
  )
  const referenceVideos = normalizedReferences.filter((value) =>
    /\.mp4(?:$|[?#])/i.test(value)
  )
  const referenceImages = [
    ...referenceFiles.map((file) => `本地文件：${file.name}`),
    ...normalizedReferences.filter(
      (value) => !/\.(?:mp3|wav|mp4)(?:$|[?#])/i.test(value)
    ),
  ]
  const modeLabels: Record<VideoFormValues['mode'], string> = {
    text_with_reference: '文本与参考素材',
    start_end_frame: '首尾帧',
  }
  const describeItems = (items: string[]) =>
    items.length > 0 ? items.join('、') : '无'
  const startFrame =
    (startFrameFile && `本地文件：${startFrameFile.name}`) ||
    values.startImageUrl.trim() ||
    '无'
  const endFrame =
    (endFrameFile && `本地文件：${endFrameFile.name}`) ||
    values.endImageUrl.trim() ||
    '无'

  return [
    '【视频生成任务】',
    '',
    '请使用已配置的 meteor-video MCP 生成视频。',
    '',
    `模型：${values.model.trim() || '未填写'}`,
    `MCP 服务地址：${endpoint}`,
    '令牌：请将创建的【视频专用分组】下的 API Key 填入这里：YOUR_NEW_API_TOKEN',
    '提示词：',
    values.prompt.trim(),
    '',
    '参数：',
    `- 时长：${values.duration} 秒`,
    `- 分辨率：${values.resolution}`,
    `- 画幅比例：${values.aspectRatio}`,
    `- 音频：${values.audio ? '开启' : '关闭'}`,
    `- 生成模式：${modeLabels[values.mode]}`,
    '',
    `参考图片：${describeItems(referenceImages)}`,
    `参考视频：${describeItems(referenceVideos)}`,
    `首帧图片：${startFrame}`,
    `尾帧图片：${endFrame}`,
    '',
    '-------------------------------------------------------------------',
    '',
    '【AI 执行须知】',
    '',
    '你是 MCP 视频生成执行器。',
    '',
    '1. 仅使用当前客户端的 meteor-video MCP；不得改用 meteor-image。若尚未配置或鉴权失败，用上述服务地址和令牌完成配置后再继续。',
    '2. 仅在当前会话中使用和记住令牌，不得把令牌写入项目代码、日志或最终回复。',
    '3. 先确认以下工具可用：create_video、get_video、create_material_upload。',
    '4. 如果提示词为空，只向用户索要提示词，不得猜测或提交。',
    '5. 如果有本地图片：先调用 create_material_upload；使用返回的 upload_url、method 和全部 headers 上传原始文件；将 material_id 传给 create_video；不要把本地路径直接传给 create_video。Seedance 普通参考图按顺序对应 @image1、@image2……，提示词应明确引用实际使用的图片。',
    '6. 严格使用用户填写的模型和参数，不要自行修改；“无”表示对应参数留空。',
    '7. 如果参数不符合模型支持范围，应先提示用户修正，不要提交。',
    '8. create_video 返回 task_id 后，定期调用 get_video，直到 SUCCESS 或 FAILURE。',
    '9. 成功时返回视频地址；失败时返回明确错误原因。',
    '10. 不要在同一用户上一个图片或视频任务结束前提交新的同类任务。',
    '11. 如没有额外指定，会话下的默认视频生成方式不变。',
    '',
    '注意：复制内容不包含本地文件字节。如上面显示本地文件，请在 Codex/Claude 中重新附加同名文件。',
  ].join('\n')
}

function useAuthenticatedVideoURL(task: VideoTask) {
  const [retryKey, setRetryKey] = useState(0)
  const [state, setState] = useState<{
    taskId: string
    url: string
    contentType: string
    loading: boolean
    error: string
  }>({ taskId: '', url: '', contentType: '', loading: false, error: '' })

  useEffect(() => {
    if (task.status !== 'SUCCESS' || !task.task_id) {
      setState({
        taskId: task.task_id,
        url: '',
        contentType: '',
        loading: false,
        error: '',
      })
      return
    }

    const controller = new AbortController()
    let objectURL = ''
    setState({
      taskId: task.task_id,
      url: '',
      contentType: '',
      loading: true,
      error: '',
    })

    void getVideoContent(task.task_id, controller.signal)
      .then((blob) => {
        if (controller.signal.aborted) return
        if (!blob.size) throw new Error('The video response was empty')
        objectURL = URL.createObjectURL(blob)
        setState({
          taskId: task.task_id,
          url: objectURL,
          contentType: blob.type,
          loading: false,
          error: '',
        })
      })
      .catch((error) => {
        if (controller.signal.aborted) return
        setState({
          taskId: task.task_id,
          url: '',
          contentType: '',
          loading: false,
          error: errorMessage(error),
        })
      })

    return () => {
      controller.abort()
      if (objectURL) URL.revokeObjectURL(objectURL)
    }
  }, [retryKey, task.status, task.task_id])

  const currentState =
    state.taskId === task.task_id
      ? state
      : {
          taskId: task.task_id,
          url: '',
          contentType: '',
          loading: true,
          error: '',
        }

  return {
    ...currentState,
    retry: () => setRetryKey((value) => value + 1),
  }
}

function VideoResult({ task }: { task: VideoTask }) {
  const { t } = useTranslation()
  const failureHint = upstreamFailureHint(task.fail_reason || '')
  const video = useAuthenticatedVideoURL(task)

  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <div className='flex items-center gap-2'>
          <Badge variant={statusVariant(task.status)}>{t(task.status)}</Badge>
          <span className='text-muted-foreground font-mono text-xs'>
            {task.task_id}
          </span>
        </div>
        <span className='text-muted-foreground text-xs'>
          {taskTimestamp(task)}
        </span>
      </div>

      {task.status === 'SUCCESS' && Boolean(task.total_tokens) && (
        <div className='bg-muted/50 flex flex-wrap items-center justify-between gap-2 rounded-md border px-3 py-2 text-xs'>
          <span className='text-muted-foreground'>
            {t('Actual usage: {{tokens}} Tokens', {
              tokens: task.total_tokens?.toLocaleString(),
            })}
          </span>
          <span className='font-medium tabular-nums'>
            {t('Actual charge: ¥{{amount}}', {
              amount: (task.billing_amount ?? 0).toFixed(4),
            })}
          </span>
        </div>
      )}

      {!TERMINAL_STATUSES.has(task.status) && (
        <Progress value={progressValue(task.progress)} />
      )}

      {task.status === 'SUCCESS' && video.loading && (
        <div className='text-muted-foreground flex aspect-video w-full flex-col items-center justify-center gap-3 rounded-lg bg-black/90 text-center'>
          <LoaderCircle className='size-8 animate-spin' />
          <span className='text-sm'>{t('Loading video content')}</span>
        </div>
      )}

      {task.status === 'SUCCESS' && video.error && (
        <Alert variant='destructive'>
          <AlertTitle>{t('Video content could not be loaded')}</AlertTitle>
          <AlertDescription>
            <span className='block'>
              {video.error ||
                t(
                  'The task succeeded, but the authenticated video content request failed'
                )}
            </span>
            <Button
              className='mt-3'
              size='sm'
              type='button'
              variant='outline'
              onClick={video.retry}
            >
              {t('Retry')}
            </Button>
          </AlertDescription>
        </Alert>
      )}

      {task.status === 'SUCCESS' && video.url && (
        <>
          <video
            className='aspect-video w-full rounded-lg bg-black object-contain'
            src={video.url}
            controls
            preload='metadata'
          />
          <div className='flex flex-wrap gap-2'>
            <Button
              variant='outline'
              render={<a href={video.url} target='_blank' rel='noreferrer' />}
            >
              <ExternalLink />
              {t('Open video')}
            </Button>
            <Button
              variant='outline'
              render={
                <a
                  href={video.url}
                  download={videoFileName(task, video.contentType)}
                  rel='noopener'
                />
              }
            >
              <Download />
              {t('Save video')}
            </Button>
          </div>
        </>
      )}

      {task.status === 'FAILURE' && (
        <Alert variant='destructive'>
          <AlertTitle>{t('Video generation failed')}</AlertTitle>
          <AlertDescription>
            <span className='block'>
              {task.fail_reason || t('Please try again later')}
            </span>
            {failureHint && (
              <span className='mt-2 block text-xs'>{t(failureHint)}</span>
            )}
          </AlertDescription>
        </Alert>
      )}
    </div>
  )
}

export function VideoGeneration() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [restoredTask, setRestoredTask] = useState<VideoTask | null>(
    () => readGenerationRecord<VideoTask>('video-current-task')?.value ?? null
  )
  const [currentTaskId, setCurrentTaskId] = useState(() => {
    return (
      readGenerationRecord<string>('video-current-task-id')?.value ??
      readGenerationRecord<VideoTask>('video-current-task')?.value.task_id ??
      ''
    )
  })
  const [localHistory, setLocalHistory] = useState<VideoTask[]>(() =>
    readGenerationHistory<VideoTask>('video-history')
  )
  const [referenceFiles, setReferenceFiles] = useState<File[]>([])
  const [startFrameFile, setStartFrameFile] = useState<File | null>(null)
  const [endFrameFile, setEndFrameFile] = useState<File | null>(null)
  const [isUploading, setIsUploading] = useState(false)
  const [downloadingTaskId, setDownloadingTaskId] = useState('')
  const { copyToClipboard } = useCopyToClipboard({
    successMessage: t('MCP prompt copied'),
  })

  const form = useForm<VideoFormValues>({
    resolver: zodResolver(videoFormSchema),
    defaultValues: {
      model: '',
      prompt: '',
      duration: 5,
      resolution: '720p',
      aspectRatio: '9:16',
      mode: 'text_with_reference',
      audio: true,
      referenceUrls: '',
      startImageUrl: '',
      endImageUrl: '',
    },
  })

  const saveVideoTask = async (task: VideoTask) => {
    if (!task.task_id || downloadingTaskId) return
    setDownloadingTaskId(task.task_id)
    try {
      const blob = await getVideoContent(task.task_id)
      if (!blob.size) throw new Error(t('The video response was empty'))
      downloadVideoBlob(blob, videoFileName(task, blob.type))
      toast.success(t('Video download started'))
    } catch (error) {
      toast.error(errorMessage(error) || t('Video download failed'))
    } finally {
      setDownloadingTaskId('')
    }
  }

  const model = form.watch('model')
  const resolution = form.watch('resolution')
  const duration = form.watch('duration')
  const mode = form.watch('mode')
  const audio = form.watch('audio')
  const referenceUrls = form.watch('referenceUrls')
  const modelsQuery = useQuery({
    queryKey: ['video-generation-models'],
    queryFn: getVideoModels,
    retry: false,
    refetchInterval: 30_000,
  })
  const selectedModel = modelsQuery.data?.find((item) => item.id === model)
  const selectedKind = modelKindForSelection(model, selectedModel?.kind)
  const selectedTier = selectedModel?.tier ?? inferredModelTier(model)
  const selectedBillingMode =
    selectedModel?.billing_mode ?? inferredBillingMode(model)
  const isGrokVideo = selectedKind === 'grok-video'
  const maxReferenceImages = isGrokVideo ? 3 : MAX_VIDEO_REFERENCE_IMAGES
  const allowedResolutions = useMemo(
    () =>
      resolutionsForModel(
        selectedKind,
        selectedTier,
        selectedModel?.resolutions
      ),
    [selectedKind, selectedModel?.resolutions, selectedTier]
  )
  const allowedAspectRatios = useMemo(
    () => aspectRatiosForKind(selectedKind),
    [selectedKind]
  )
  const minimumDuration = minimumDurationForKind(selectedKind)
  const klingOmniReferenceVideo =
    selectedKind === 'kling-v3-omni' && hasReferenceVideo(referenceUrls)
  let modelDescription = t(
    'No available video models are configured in Channels'
  )
  if (modelsQuery.isLoading) {
    modelDescription = t('Loading models from Channels')
  } else if (modelsQuery.data?.length) {
    modelDescription = t('{{count}} available video models', {
      count: modelsQuery.data.length,
    })
  }

  useEffect(() => {
    const firstModel = modelsQuery.data?.[0]
    if (!firstModel) return
    const currentModel = form.getValues('model')
    if (!modelsQuery.data?.some((item) => item.id === currentModel)) {
      form.setValue('model', firstModel.id)
    }
  }, [form, modelsQuery.data])

  useEffect(() => {
    if (!allowedResolutions.includes(resolution)) {
      form.setValue('resolution', '720p')
    }
  }, [allowedResolutions, form, resolution])

  useEffect(() => {
    if (!allowedAspectRatios.includes(form.getValues('aspectRatio'))) {
      form.setValue('aspectRatio', allowedAspectRatios[0] ?? '16:9')
    }
  }, [allowedAspectRatios, form])

  useEffect(() => {
    if (klingOmniReferenceVideo && form.getValues('audio')) {
      form.setValue('audio', false, {
        shouldDirty: true,
        shouldValidate: true,
      })
    }
  }, [form, klingOmniReferenceVideo])

  useEffect(() => {
    if (isGrokVideo && form.getValues('mode') === 'start_end_frame') {
      form.setValue('mode', 'text_with_reference', {
        shouldDirty: true,
        shouldValidate: true,
      })
    }
  }, [form, isGrokVideo])

  const applyModelDefaults = (nextModel: string) => {
    const nextSelectedModel = modelsQuery.data?.find(
      (item) => item.id === nextModel
    )
    const nextKind = modelKindForSelection(nextModel, nextSelectedModel?.kind)
    const nextTier = nextSelectedModel?.tier ?? inferredModelTier(nextModel)
    const nextResolutions = resolutionsForModel(
      nextKind,
      nextTier,
      nextSelectedModel?.resolutions
    )
    const nextAspectRatios = aspectRatiosForKind(nextKind)
    const currentResolution = form.getValues('resolution')
    const currentAspectRatio = form.getValues('aspectRatio')
    const currentDuration = form.getValues('duration')

    // Normalize all model-dependent values in the same event as the model
    // change. This prevents a stale 480p value from being submitted during
    // the render/effect gap when switching from Seedance to Kling.
    if (!nextResolutions.includes(currentResolution)) {
      form.setValue('resolution', nextResolutions[0] ?? '720p', {
        shouldDirty: true,
        shouldValidate: true,
      })
    }
    if (!nextAspectRatios.includes(currentAspectRatio)) {
      form.setValue('aspectRatio', nextAspectRatios[0] ?? '16:9', {
        shouldDirty: true,
        shouldValidate: true,
      })
    }
    if (isKlingKind(nextKind) && form.getValues('audio')) {
      // Kling documents silent output as its default. Users can turn audio
      // back on after the model switch when the selected variant supports it.
      form.setValue('audio', false, {
        shouldDirty: true,
        shouldValidate: true,
      })
    }
    if (
      nextKind === 'grok-video' &&
      form.getValues('mode') === 'start_end_frame'
    ) {
      form.setValue('mode', 'text_with_reference', {
        shouldDirty: true,
        shouldValidate: true,
      })
    }
    const nextMinimumDuration = minimumDurationForKind(nextKind)
    if (
      Number.isFinite(currentDuration) &&
      currentDuration < nextMinimumDuration
    ) {
      form.setValue('duration', nextMinimumDuration, {
        shouldDirty: true,
        shouldValidate: true,
      })
    }
  }

  const estimatedPricePerSecond = estimateVideoPricePerSecond(
    selectedKind,
    selectedTier,
    resolution,
    audio,
    referenceUrls,
    selectedModel?.price_per_second
  )

  const currentTaskQuery = useQuery({
    queryKey: ['video-task', currentTaskId],
    queryFn: () => getVideoTask(currentTaskId),
    enabled: currentTaskId !== '',
    refetchInterval: (query) => {
      const status = query.state.data?.data.status
      return status && TERMINAL_STATUSES.has(status) ? false : 4000
    },
  })

  useEffect(() => {
    const task = currentTaskQuery.data?.data
    if (!task) return
    setRestoredTask(task)
    setLocalHistory((previous) => mergeVideoTasks(previous, [task]))
    writeGenerationRecord('video-current-task', task)
  }, [currentTaskQuery.data])

  const historyQuery = useQuery({
    queryKey: ['video-tasks'],
    queryFn: getVideoTasks,
    refetchInterval: 10000,
  })

  useEffect(() => {
    const serverHistory = historyQuery.data?.data?.items ?? []
    if (serverHistory.length === 0) return
    setLocalHistory((previous) => mergeVideoTasks(previous, serverHistory))
  }, [historyQuery.data])

  useEffect(() => {
    writeGenerationHistory('video-history', localHistory)
  }, [localHistory])

  const createMutation = useMutation({
    mutationFn: createVideoTracked,
    onSuccess: (response) => {
      const taskId = response.task_id || response.id
      setCurrentTaskId(taskId)
      setRestoredTask(null)
      writeGenerationRecord('video-current-task-id', taskId)
      toast.success(t('Video task submitted'))
      void queryClient.invalidateQueries({ queryKey: ['video-tasks'] })
    },
    onError: (error) => {
      const message = errorMessage(error)
      const hint = upstreamFailureHint(message)
      toast.error(
        hint
          ? `${message}\n${t(hint)}`
          : message || t('Video task submission failed')
      )
    },
  })

  const onSubmit = async (values: VideoFormValues) => {
    const taskAtSubmit = currentTaskQuery.data?.data ?? restoredTask
    if (
      createMutation.isPending ||
      isUploading ||
      (taskAtSubmit && !TERMINAL_STATUSES.has(taskAtSubmit.status))
    ) {
      toast.error(
        t('Wait for the current video task to finish before submitting another')
      )
      return
    }
    if (referenceFiles.length > maxReferenceImages) {
      toast.error(
        t('Select no more than {{count}} reference images', {
          count: maxReferenceImages,
        })
      )
      return
    }
    const isKling = isKlingKind(selectedKind)
    if (isGrokVideo && values.mode === 'start_end_frame') {
      toast.error(
        t('Grok video does not support separate start and end frames')
      )
      return
    }
    if (!allowedResolutions.includes(values.resolution)) {
      toast.error(
        t('Resolution {{resolution}} is not supported by the selected model', {
          resolution: values.resolution,
        })
      )
      return
    }
    const requiresBothFrames = !isKling
    if (
      values.mode === 'start_end_frame' &&
      requiresBothFrames &&
      !startFrameFile &&
      !values.startImageUrl.trim()
    ) {
      toast.error(t('Select a start frame image or enter its URL'))
      return
    }
    if (
      values.mode === 'start_end_frame' &&
      requiresBothFrames &&
      !endFrameFile &&
      !values.endImageUrl.trim()
    ) {
      toast.error(t('Select an end frame image or enter its URL'))
      return
    }
    setIsUploading(true)
    try {
      const referenceImages = splitReferenceUrls(values.referenceUrls)
      const request: VideoGenerationRequest = {
        model: values.model,
        prompt: values.prompt.trim(),
        duration: values.duration,
        resolution: values.resolution,
        aspect_ratio: values.aspectRatio,
        mode: values.mode,
        audio: values.audio,
      }
      if (referenceImages.length > 0) {
        request.reference_images = referenceImages
      }
      if (values.mode === 'text_with_reference' && referenceFiles.length > 0) {
        request.reference_material_ids = await Promise.all(
          referenceFiles.map(uploadMaterial)
        )
      }
      if (values.mode === 'start_end_frame') {
        if (startFrameFile) {
          request.start_material_id = await uploadMaterial(startFrameFile)
        } else {
          request.start_image_url = values.startImageUrl.trim()
        }
        if (endFrameFile) {
          request.end_material_id = await uploadMaterial(endFrameFile)
        } else {
          request.end_image_url = values.endImageUrl.trim()
        }
      }
      createMutation.mutate(request)
    } catch (error) {
      toast.error(error instanceof Error ? error.message : t('Upload failed'))
    } finally {
      setIsUploading(false)
    }
  }

  useEffect(() => {
    const task = currentTaskQuery.data?.data ?? restoredTask
    const isGenerating =
      createMutation.isPending ||
      isUploading ||
      Boolean(task && !TERMINAL_STATUSES.has(task.status))
    if (!isGenerating) return

    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', handleBeforeUnload)
    return () => window.removeEventListener('beforeunload', handleBeforeUnload)
  }, [
    createMutation.isPending,
    currentTaskQuery.data,
    isUploading,
    restoredTask,
  ])

  const copyMcpPrompt = () => {
    void copyToClipboard(
      buildMcpPrompt(
        form.getValues(),
        referenceFiles,
        startFrameFile,
        endFrameFile
      )
    )
  }

  const currentTask = currentTaskQuery.data?.data ?? restoredTask
  const hasActiveVideoTask = Boolean(
    currentTask && !TERMINAL_STATUSES.has(currentTask.status)
  )
  const history = useMemo(
    () => mergeVideoTasks(localHistory, historyQuery.data?.data?.items ?? []),
    [historyQuery.data?.data?.items, localHistory]
  )

  return (
    <Main className='space-y-6 overflow-x-hidden overflow-y-auto px-3 py-6 pb-12 sm:px-4'>
      <div>
        <h1 className='flex items-center gap-2 text-2xl font-semibold'>
          <Film className='size-6' />
          {t('Video Generation')}
        </h1>
        <p className='text-muted-foreground mt-1 text-sm'>
          {t('Create asynchronous videos and track task status')}
        </p>
      </div>

      <div className='grid gap-6 xl:grid-cols-[minmax(0,5fr)_minmax(360px,4fr)]'>
        <Card>
          <CardHeader>
            <CardTitle>{t('Generation settings')}</CardTitle>
            <CardAction>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={copyMcpPrompt}
              >
                <Copy />
                {t('Copy MCP prompt')}
              </Button>
            </CardAction>
            <CardDescription>
              {t(
                'Upload local images directly to temporary OSS storage, or use public HTTP/HTTPS URLs'
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Form {...form}>
              <form
                className='space-y-5'
                onSubmit={form.handleSubmit(onSubmit)}
              >
                <div className='grid gap-4 md:grid-cols-2'>
                  <div className='space-y-4'>
                    <FormField
                      control={form.control}
                      name='model'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Model')}</FormLabel>
                          <FormControl>
                            <NativeSelect
                              className='w-full'
                              disabled={
                                modelsQuery.isLoading ||
                                (modelsQuery.data?.length ?? 0) === 0
                              }
                              value={field.value}
                              onChange={(event) => {
                                const nextModel = event.target.value
                                field.onChange(nextModel)
                                applyModelDefaults(nextModel)
                              }}
                            >
                              {(modelsQuery.data ?? []).map((item) => (
                                <NativeSelectOption
                                  key={item.id}
                                  value={item.id}
                                >
                                  {item.id}
                                </NativeSelectOption>
                              ))}
                            </NativeSelect>
                          </FormControl>
                          <FormDescription>{modelDescription}</FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='duration'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Duration (seconds)')}</FormLabel>
                          <FormControl>
                            <Input
                              type='number'
                              min={minimumDuration}
                              max={15}
                              value={field.value}
                              onChange={(event) =>
                                field.onChange(event.target.valueAsNumber)
                              }
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              minimumDuration === 3
                                ? 'Allowed range: 3–15'
                                : 'Allowed range: 4–15'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>

                  <div className='space-y-4'>
                    <FormField
                      control={form.control}
                      name='resolution'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Resolution')}</FormLabel>
                          <FormControl>
                            <NativeSelect
                              className='w-full'
                              value={field.value}
                              onChange={field.onChange}
                            >
                              {allowedResolutions.map((item) => (
                                <NativeSelectOption key={item} value={item}>
                                  {item}
                                </NativeSelectOption>
                              ))}
                            </NativeSelect>
                          </FormControl>
                          {isKlingKind(selectedKind) && (
                            <FormDescription>
                              {t(
                                'Kling maps 720p, 1080p, and 4K to std, pro, and 4k automatically'
                              )}
                            </FormDescription>
                          )}
                          <FormMessage />
                        </FormItem>
                      )}
                    />

                    <FormField
                      control={form.control}
                      name='aspectRatio'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Aspect ratio')}</FormLabel>
                          <FormControl>
                            <NativeSelect
                              className='w-full'
                              value={field.value}
                              onChange={field.onChange}
                            >
                              {allowedAspectRatios.map((item) => (
                                <NativeSelectOption key={item} value={item}>
                                  {item}
                                </NativeSelectOption>
                              ))}
                            </NativeSelect>
                          </FormControl>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                </div>

                <FormField
                  control={form.control}
                  name='prompt'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Prompt')}</FormLabel>
                      <FormControl>
                        <Textarea
                          className='min-h-32'
                          maxLength={1300}
                          placeholder={t(
                            'Describe the scene, motion, camera, and style'
                          )}
                          {...field}
                        />
                      </FormControl>
                      <FormDescription>
                        {t('{{count}} / 1300 characters', {
                          count: field.value.length,
                        })}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <div className='grid gap-4 md:grid-cols-2'>
                  <FormField
                    control={form.control}
                    name='mode'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Generation mode')}</FormLabel>
                        <FormControl>
                          <NativeSelect
                            className='w-full'
                            value={field.value}
                            onChange={field.onChange}
                          >
                            <NativeSelectOption value='text_with_reference'>
                              {t('Text with references')}
                            </NativeSelectOption>
                            {!isGrokVideo && (
                              <NativeSelectOption value='start_end_frame'>
                                {t('Start and end frames')}
                              </NativeSelectOption>
                            )}
                          </NativeSelect>
                        </FormControl>
                        {isKlingKind(selectedKind) && (
                          <FormDescription>
                            {t(
                              'Kling uses the selected resolution as its generation mode'
                            )}
                          </FormDescription>
                        )}
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='audio'
                    render={({ field }) => (
                      <FormItem className='rounded-lg border p-3'>
                        <div className='flex items-center justify-between gap-4'>
                          <div>
                            <FormLabel>
                              <Volume2 className='size-4' />
                              {t('Generate audio')}
                            </FormLabel>
                            <FormDescription>
                              {t(audioDescriptionForKind(selectedKind))}
                            </FormDescription>
                          </div>
                          <FormControl>
                            <Switch
                              checked={field.value}
                              disabled={klingOmniReferenceVideo || isGrokVideo}
                              onCheckedChange={field.onChange}
                            />
                          </FormControl>
                        </div>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>

                {mode === 'text_with_reference' && (
                  <div className='space-y-4'>
                    <FormItem>
                      <FormLabel>{t('Upload reference images')}</FormLabel>
                      <FormControl>
                        <Input
                          type='file'
                          accept='image/jpeg,image/png,image/webp'
                          multiple
                          onChange={(event) => {
                            const selected = [
                              ...(event.currentTarget.files ?? []),
                            ]
                            event.currentTarget.value = ''
                            if (selected.length === 0) return

                            const next = [
                              ...new Map(
                                [...referenceFiles, ...selected].map(
                                  (file) =>
                                    [videoReferenceFileKey(file), file] as const
                                )
                              ).values(),
                            ]
                            if (next.length > maxReferenceImages) {
                              toast.error(
                                t(
                                  'Select no more than {{count}} reference images',
                                  {
                                    count: maxReferenceImages,
                                  }
                                )
                              )
                              return
                            }
                            if (
                              !selected.every(isSupportedVideoReferenceImage)
                            ) {
                              toast.error(
                                t(
                                  'Only JPG, PNG, and WEBP images are supported'
                                )
                              )
                              return
                            }
                            if (
                              selected.some(
                                (file) =>
                                  file.size > MAX_VIDEO_REFERENCE_IMAGE_SIZE
                              )
                            ) {
                              toast.error(
                                t('Each reference image must not exceed 10 MiB')
                              )
                              return
                            }
                            setReferenceFiles(next)
                          }}
                        />
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Upload up to {{count}} static JPG, PNG, or WEBP images, up to 10 MiB each. Select files again to append.',
                          { count: maxReferenceImages }
                        )}
                        {referenceFiles.length > 0 && (
                          <span className='ml-1'>
                            {t('{{count}} image(s) selected', {
                              count: referenceFiles.length,
                            })}
                          </span>
                        )}
                      </FormDescription>
                    </FormItem>

                    {referenceFiles.length > 0 && (
                      <div className='space-y-2 rounded-md border p-3'>
                        {referenceFiles.map((file) => (
                          <div
                            key={videoReferenceFileKey(file)}
                            className='flex items-center justify-between gap-3'
                          >
                            <span className='min-w-0 truncate text-sm'>
                              {file.name}
                            </span>
                            <Button
                              type='button'
                              size='icon-sm'
                              variant='ghost'
                              aria-label={t('Remove {{name}}', {
                                name: file.name,
                              })}
                              disabled={createMutation.isPending || isUploading}
                              onClick={() =>
                                setReferenceFiles((files) =>
                                  files.filter(
                                    (candidate) => candidate !== file
                                  )
                                )
                              }
                            >
                              <X />
                            </Button>
                          </div>
                        ))}
                      </div>
                    )}
                    <FormField
                      control={form.control}
                      name='referenceUrls'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>
                            {t('Additional reference URLs')}
                          </FormLabel>
                          <FormControl>
                            <Textarea
                              className='min-h-24 font-mono text-xs'
                              placeholder={[
                                'https://cdn.example.com/reference.jpg',
                                'reference:https://cdn.example.com/reference.mp4',
                              ].join('\n')}
                              {...field}
                            />
                          </FormControl>
                          <FormDescription>
                            {t(
                              'Optional. One URL per line; audio and MP4 references still require a URL.'
                            )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                )}

                {mode === 'start_end_frame' && (
                  <div className='grid gap-4 md:grid-cols-2'>
                    <FormField
                      control={form.control}
                      name='startImageUrl'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('Start frame')}</FormLabel>
                          <FormControl>
                            <div className='space-y-2'>
                              <Input
                                type='file'
                                accept='image/jpeg,image/png,image/webp'
                                onChange={(event) =>
                                  setStartFrameFile(
                                    event.target.files?.[0] ?? null
                                  )
                                }
                              />
                              <Input
                                type='url'
                                disabled={startFrameFile !== null}
                                placeholder={t('Or enter a public image URL')}
                                {...field}
                              />
                            </div>
                          </FormControl>
                          <FormDescription>
                            {startFrameFile?.name ??
                              t(
                                'A selected local file takes priority over URL'
                              )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                    <FormField
                      control={form.control}
                      name='endImageUrl'
                      render={({ field }) => (
                        <FormItem>
                          <FormLabel>{t('End frame')}</FormLabel>
                          <FormControl>
                            <div className='space-y-2'>
                              <Input
                                type='file'
                                accept='image/jpeg,image/png,image/webp'
                                onChange={(event) =>
                                  setEndFrameFile(
                                    event.target.files?.[0] ?? null
                                  )
                                }
                              />
                              <Input
                                type='url'
                                disabled={endFrameFile !== null}
                                placeholder={t('Or enter a public image URL')}
                                {...field}
                              />
                            </div>
                          </FormControl>
                          <FormDescription>
                            {endFrameFile?.name ??
                              t(
                                'A selected local file takes priority over URL'
                              )}
                          </FormDescription>
                          <FormMessage />
                        </FormItem>
                      )}
                    />
                  </div>
                )}

                <VideoPriceComparison
                  billingMode={selectedBillingMode}
                  pricingReference={selectedModel?.pricing_reference ?? model}
                  kind={selectedKind}
                  tier={selectedTier}
                  resolution={resolution}
                  audio={audio}
                  hasInputVideo={hasReferenceVideo(referenceUrls)}
                  duration={duration}
                  configuredTokenBasePrice={
                    selectedModel?.price_per_million_tokens
                  }
                  currentPerSecondPrice={estimatedPricePerSecond}
                />

                <Button
                  className='w-full'
                  size='lg'
                  type='submit'
                  disabled={
                    createMutation.isPending ||
                    isUploading ||
                    hasActiveVideoTask ||
                    (modelsQuery.data?.length ?? 0) === 0
                  }
                >
                  {createMutation.isPending || isUploading ? (
                    <LoaderCircle className='animate-spin' />
                  ) : (
                    <ImagePlus />
                  )}
                  {isUploading ? t('Uploading images') : t('Generate video')}
                </Button>
              </form>
            </Form>
          </CardContent>
        </Card>

        <div className='space-y-6'>
          <Card>
            <CardHeader>
              <CardTitle>{t('Current task')}</CardTitle>
              <CardDescription>
                {t('The page polls task status automatically')}
              </CardDescription>
            </CardHeader>
            <CardContent>
              {currentTask ? (
                <VideoResult task={currentTask} />
              ) : (
                <div className='text-muted-foreground flex min-h-52 flex-col items-center justify-center gap-3 text-center'>
                  <Film className='size-10 opacity-40' />
                  <p>{t('Submit a task to see the generated video here')}</p>
                </div>
              )}
            </CardContent>
          </Card>

          {currentTask && !TERMINAL_STATUSES.has(currentTask.status) && (
            <Alert>
              <LoaderCircle className='animate-spin' />
              <AlertTitle>{t('Video task is still processing')}</AlertTitle>
              <AlertDescription>
                {t(
                  'You can switch sections or refresh; this task is saved locally and will resume polling when you return'
                )}
              </AlertDescription>
            </Alert>
          )}

          {restoredTask && !currentTaskQuery.data?.data && (
            <Alert>
              <Film />
              <AlertDescription>
                {t(
                  'This video task was restored from this browser; signed result links expire after 24 hours'
                )}
              </AlertDescription>
            </Alert>
          )}

          <Card>
            <CardHeader>
              <CardTitle>{t('Recent video tasks')}</CardTitle>
              <CardDescription>
                {t(
                  'Your latest video generation requests are kept in this browser for up to 24 hours'
                )}
              </CardDescription>
            </CardHeader>
            <CardContent className='max-h-[38rem] overflow-y-auto'>
              {history.length === 0 ? (
                <p className='text-muted-foreground py-8 text-center text-sm'>
                  {t('No video tasks yet')}
                </p>
              ) : (
                <div className='divide-y'>
                  {history.map((task) => (
                    <div
                      className='flex flex-col gap-3 py-4 first:pt-0 last:pb-0'
                      key={task.task_id}
                    >
                      <div className='min-w-0 space-y-1'>
                        <div className='flex flex-wrap items-center gap-2'>
                          <Badge variant={statusVariant(task.status)}>
                            {t(task.status)}
                          </Badge>
                          <span className='truncate font-mono text-xs'>
                            {task.task_id}
                          </span>
                        </div>
                        <p className='text-muted-foreground truncate text-xs'>
                          {task.properties?.origin_model_name} ·{' '}
                          {taskTimestamp(task)}
                        </p>
                      </div>
                      {!TERMINAL_STATUSES.has(task.status) && (
                        <Progress value={progressValue(task.progress)} />
                      )}
                      {task.status === 'FAILURE' && task.fail_reason && (
                        <p className='text-destructive line-clamp-3 text-xs'>
                          {task.fail_reason}
                        </p>
                      )}
                      <div className='flex flex-wrap items-center gap-2'>
                        <Button
                          className='flex-1'
                          size='sm'
                          variant='secondary'
                          onClick={() => {
                            setRestoredTask(task)
                            setCurrentTaskId(task.task_id)
                          }}
                        >
                          {t('View')}
                        </Button>
                        {task.status === 'SUCCESS' && (
                          <Button
                            className='flex-1'
                            disabled={Boolean(downloadingTaskId)}
                            size='sm'
                            variant='outline'
                            onClick={() => void saveVideoTask(task)}
                          >
                            {downloadingTaskId === task.task_id ? (
                              <LoaderCircle className='animate-spin' />
                            ) : (
                              <Download />
                            )}
                            {downloadingTaskId === task.task_id
                              ? t('Saving video')
                              : t('Save video')}
                          </Button>
                        )}
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>

          <Alert>
            <Clock3 />
            <AlertTitle>{t('Video links expire after 24 hours')}</AlertTitle>
            <AlertDescription>
              {t('Open or save completed videos before the signed URL expires')}
            </AlertDescription>
          </Alert>
        </div>
      </div>
    </Main>
  )
}

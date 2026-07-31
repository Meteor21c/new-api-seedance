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
  ExternalLink,
  Film,
  ImagePlus,
  LoaderCircle,
  Volume2,
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
  getVideoModels,
  getVideoTask,
  getVideoTasks,
  uploadMaterial,
} from './api'
import {
  VIDEO_ASPECT_RATIOS,
  VIDEO_MODES,
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
  return kind === 'kling-v3' || kind === 'kling-v3-omni' ? 3 : 4
}

function isKlingKind(kind: VideoModelKind): boolean {
  return kind === 'kling-v3' || kind === 'kling-v3-omni'
}

const KLING_ASPECT_RATIOS = ['16:9', '9:16', '1:1'] as const

function aspectRatiosForKind(kind: VideoModelKind): VideoAspectRatio[] {
  if (kind === 'kling-v3' || kind === 'kling-v3-omni') {
    return [...KLING_ASPECT_RATIOS]
  }
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
  tier: VideoTier
): VideoResolution[] {
  if (kind === 'kling-v3' || kind === 'kling-v3-omni') {
    return ['720p', '1080p', '4K']
  }
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

function mcpEndpoint(): string {
  if (typeof window !== 'undefined' && window.location.origin) {
    return `${window.location.origin}/mcp`
  }
  return 'https://api.meteor21c.fun/mcp'
}

function buildMcpPrompt(
  values: VideoFormValues,
  referenceFiles: File[],
  startFrameFile: File | null,
  endFrameFile: File | null
): string {
  const endpoint = mcpEndpoint()
  const request = {
    model: values.model.trim(),
    prompt: values.prompt.trim(),
    duration: values.duration,
    resolution: values.resolution,
    aspect_ratio: values.aspectRatio,
    mode: values.mode,
    audio: values.audio,
    reference_images: splitReferenceUrls(values.referenceUrls),
    reference_material_ids: [],
    start_image_url: values.startImageUrl.trim(),
    end_image_url: values.endImageUrl.trim(),
    start_material_id: '',
    end_material_id: '',
  }
  const localMaterials = [
    ...referenceFiles.map(
      (file) =>
        `reference: ${file.name} (${file.type || 'unknown MIME type'}, ${file.size} bytes)`
    ),
    ...(startFrameFile
      ? [
          `start frame: ${startFrameFile.name} (${startFrameFile.type || 'unknown MIME type'}, ${startFrameFile.size} bytes)`,
        ]
      : []),
    ...(endFrameFile
      ? [
          `end frame: ${endFrameFile.name} (${endFrameFile.type || 'unknown MIME type'}, ${endFrameFile.size} bytes)`,
        ]
      : []),
  ]

  return [
    '请把下面内容作为一次 New API 视频 MCP 配置与生成任务执行。',
    '先完成 MCP 连接并确认工具可用；不要把令牌写入代码、日志或回复。',
    '',
    '【MCP 连接】',
    `服务地址：${endpoint}`,
    '令牌：请将 YOUR_NEW_API_TOKEN 替换成我提供的 New API 用户令牌（不要使用上游 API Key）。',
    '',
    '只执行当前客户端对应的代码块：Codex 执行 Codex 块，Claude Code 执行 Claude 块。',
    'Codex（终端执行，已有同名配置时先删除再添加）：',
    '```bash',
    "export METEOR_VIDEO_TOKEN='YOUR_NEW_API_TOKEN'",
    `codex mcp remove meteor-video >/dev/null 2>&1 || true`,
    `codex mcp add meteor-video --url '${endpoint}' --bearer-token-env-var METEOR_VIDEO_TOKEN`,
    'codex mcp list',
    '```',
    '',
    'Claude Code（终端执行）：',
    '```bash',
    "export METEOR_VIDEO_TOKEN='YOUR_NEW_API_TOKEN'",
    `claude mcp add --transport http meteor-video '${endpoint}' --header "Authorization: Bearer \${METEOR_VIDEO_TOKEN}"`,
    'claude mcp list',
    '```',
    '',
    '重启或刷新 Codex/Claude，确认出现 create_video、get_video、create_material_upload；图片任务还可使用 create_image。',
    '连接成功后，若本次提示词不为空就调用 create_video；若提示词为空，先向我索要提示词，不要猜测或直接提交。',
    'create_video 返回 task_id 后，每隔数秒调用 get_video，直到 SUCCESS 或 FAILURE；成功时返回 result_url。',
    '',
    '【本次视频参数】',
    '严格按以下 JSON 传给 create_video；空字符串和空数组表示当前未填写，不要自行补全。',
    '```json',
    JSON.stringify(request, null, 2),
    '```',
    '',
    '【本地素材】',
    localMaterials.length > 0
      ? localMaterials.join('\n')
      : '（空；没有选择本地素材）',
    '复制的文字不包含本地文件字节。若要使用上面的本地文件，请在 Codex/Claude 中重新附加文件，然后先调用 create_material_upload，按返回的 upload_url、method 和全部 headers 用 HTTP PUT 上传原始字节，再把返回的 material_id 放入对应的 material_id 参数；不要把本地路径传给 create_video。',
  ].join('\n')
}

function VideoResult({ task }: { task: VideoTask }) {
  const { t } = useTranslation()
  const failureHint = upstreamFailureHint(task.fail_reason || '')

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

      {!TERMINAL_STATUSES.has(task.status) && (
        <Progress value={progressValue(task.progress)} />
      )}

      {task.status === 'SUCCESS' && task.result_url && (
        <>
          <video
            className='aspect-video w-full rounded-lg bg-black object-contain'
            src={task.result_url}
            controls
            preload='metadata'
          />
          <Button
            variant='outline'
            render={
              <a href={task.result_url} target='_blank' rel='noreferrer' />
            }
          >
            <ExternalLink />
            {t('Open video')}
          </Button>
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
  })
  const selectedModel = modelsQuery.data?.find((item) => item.id === model)
  const selectedKind = modelKindForSelection(model, selectedModel?.kind)
  const selectedTier = selectedModel?.tier ?? inferredModelTier(model)
  const allowedResolutions = useMemo(
    () => resolutionsForModel(selectedKind, selectedTier),
    [selectedKind, selectedTier]
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

  const applyModelDefaults = (nextModel: string) => {
    const nextSelectedModel = modelsQuery.data?.find(
      (item) => item.id === nextModel
    )
    const nextKind = modelKindForSelection(nextModel, nextSelectedModel?.kind)
    const nextTier = nextSelectedModel?.tier ?? inferredModelTier(nextModel)
    const nextResolutions = resolutionsForModel(nextKind, nextTier)
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

  const estimatedPrice = useMemo(() => {
    const pricePerSecond = isKlingKind(selectedKind)
      ? estimateKlingPrice(selectedKind, resolution, audio, referenceUrls)
      : (PRICE_PER_SECOND[selectedTier][resolution] ?? 0)
    return pricePerSecond * (Number.isFinite(duration) ? duration : 0)
  }, [audio, duration, referenceUrls, resolution, selectedKind, selectedTier])

  const estimatedPricePerSecond = isKlingKind(selectedKind)
    ? estimateKlingPrice(selectedKind, resolution, audio, referenceUrls)
    : (PRICE_PER_SECOND[selectedTier][resolution] ?? 0)

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
    const isKling = isKlingKind(selectedKind)
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
                            <NativeSelectOption value='start_end_frame'>
                              {t('Start and end frames')}
                            </NativeSelectOption>
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
                              {t(
                                selectedKind === 'kling-v3' ||
                                  selectedKind === 'kling-v3-omni'
                                  ? 'Audio changes the listed price'
                                  : 'Audio does not change the listed price'
                              )}
                            </FormDescription>
                          </div>
                          <FormControl>
                            <Switch
                              checked={field.value}
                              disabled={klingOmniReferenceVideo}
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
                          onChange={(event) =>
                            setReferenceFiles(
                              [...(event.target.files ?? [])].slice(
                                0,
                                selectedKind === 'kling-v3' ? 2 : 9
                              )
                            )
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {referenceFiles.length > 0
                          ? t('{{count}} image(s) selected', {
                              count: referenceFiles.length,
                            })
                          : t(
                              'JPG, PNG, or WEBP; up to 10 MiB each. Files upload directly to OSS.'
                            )}
                      </FormDescription>
                    </FormItem>
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

                <div className='bg-muted/50 flex flex-wrap items-center justify-between gap-3 rounded-lg border p-4'>
                  <div>
                    <p className='text-sm font-medium'>
                      {t('Estimated price')}
                    </p>
                    <p className='text-muted-foreground text-xs'>
                      {t('Calculated with the configured 1:1 billing ratio')}
                    </p>
                  </div>
                  <div className='text-right'>
                    <p className='text-xl font-semibold tabular-nums'>
                      ¥{estimatedPrice.toFixed(4)}
                    </p>
                    <p className='text-muted-foreground text-xs'>
                      ¥{estimatedPricePerSecond.toFixed(4)}
                      {' / '}
                      {t('second')}
                    </p>
                  </div>
                </div>

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
                        {task.status === 'SUCCESS' && task.result_url && (
                          <Button
                            className='flex-1'
                            size='sm'
                            variant='outline'
                            render={
                              <a
                                href={task.result_url}
                                target='_blank'
                                rel='noreferrer'
                              />
                            }
                          >
                            <ExternalLink />
                            {t('Open')}
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

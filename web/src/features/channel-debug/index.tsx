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

import { useMutation, useQuery } from '@tanstack/react-query'
import {
  Bot,
  Check,
  ChevronRight,
  CircleDollarSign,
  Clock3,
  Copy,
  DatabaseZap,
  Eraser,
  Gauge,
  LoaderCircle,
  MessagesSquare,
  RefreshCw,
  Search,
  Send,
  Server,
  Sparkles,
  TimerReset,
  User,
  Zap,
} from 'lucide-react'
import { useEffect, useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Separator } from '@/components/ui/separator'
import { Switch } from '@/components/ui/switch'
import { Textarea } from '@/components/ui/textarea'
import { getChannelTypeLabel } from '@/features/channels/lib/channel-utils'
import { formatLogQuota, formatTokens } from '@/lib/format'

import {
  getAllDebugChannels,
  getChannelDebugModels,
  runChannelDebug,
} from './api'
import type {
  ChannelDebugMessage,
  ChannelDebugRequest,
  ChannelDebugResult,
} from './types'

const DEFAULT_PROMPT = 'hello'
const DEFAULT_MAX_TOKENS = 512

type ModelOption = {
  id: string
  configured: boolean
  upstream: boolean
}

type ConversationEntry = ChannelDebugMessage & {
  id: string
}

function splitModels(value: string | null | undefined): string[] {
  return (value ?? '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

function formatDuration(milliseconds: number): string {
  if (!Number.isFinite(milliseconds) || milliseconds < 0) return '-'
  if (milliseconds < 1000) return `${Math.max(0, Math.round(milliseconds))} ms`
  return `${(milliseconds / 1000).toFixed(2)} s`
}

function MetricCard({
  icon: Icon,
  label,
  value,
  accent,
}: {
  icon: React.ElementType
  label: string
  value: string
  accent: string
}) {
  return (
    <div className='bg-card/70 rounded-xl border p-3 shadow-sm'>
      <div className='text-muted-foreground flex items-center gap-2 text-xs'>
        <span className={`grid size-7 place-items-center rounded-lg ${accent}`}>
          <Icon className='size-3.5' />
        </span>
        {label}
      </div>
      <div className='mt-2 text-xl font-semibold tracking-tight'>{value}</div>
    </div>
  )
}

function ConversationMessage({ message }: { message: ChannelDebugMessage }) {
  const isUser = message.role === 'user'
  return (
    <div className={`flex gap-3 ${isUser ? 'justify-end' : 'justify-start'}`}>
      {!isUser && (
        <div className='bg-primary/10 text-primary mt-0.5 grid size-8 shrink-0 place-items-center rounded-full'>
          <Bot className='size-4' />
        </div>
      )}
      <div
        className={`max-w-[84%] rounded-2xl px-4 py-3 text-sm leading-6 whitespace-pre-wrap ${
          isUser
            ? 'bg-primary text-primary-foreground rounded-br-md'
            : 'bg-muted/70 rounded-bl-md border'
        }`}
      >
        {message.content}
      </div>
      {isUser && (
        <div className='bg-muted mt-0.5 grid size-8 shrink-0 place-items-center rounded-full border'>
          <User className='size-4' />
        </div>
      )}
    </div>
  )
}

function ModelList({
  models,
  selected,
  onSelect,
}: {
  models: ModelOption[]
  selected: string
  onSelect: (model: string) => void
}) {
  const { t } = useTranslation()
  if (models.length === 0) {
    return (
      <div className='text-muted-foreground px-3 py-10 text-center text-sm'>
        {t('No matching models')}
      </div>
    )
  }

  return (
    <div className='space-y-1'>
      {models.map((model) => {
        const active = model.id === selected
        return (
          <button
            key={model.id}
            type='button'
            onClick={() => onSelect(model.id)}
            className={`flex w-full items-center gap-2 rounded-lg border px-3 py-2.5 text-left transition-colors ${
              active
                ? 'border-primary/50 bg-primary/8'
                : 'hover:bg-muted/70 border-transparent'
            }`}
          >
            <span
              className={`size-2 shrink-0 rounded-full ${
                model.configured ? 'bg-emerald-500' : 'bg-amber-500'
              }`}
            />
            <span className='min-w-0 flex-1 truncate font-mono text-xs'>
              {model.id}
            </span>
            {model.configured && (
              <Badge
                variant='outline'
                className='border-emerald-500/25 bg-emerald-500/8 text-[10px] text-emerald-600 dark:text-emerald-400'
              >
                {t('Enabled on site')}
              </Badge>
            )}
            {active ? (
              <Check className='text-primary size-4 shrink-0' />
            ) : (
              <ChevronRight className='text-muted-foreground size-3.5 shrink-0' />
            )}
          </button>
        )
      })}
    </div>
  )
}

function requestErrorMessage(error: unknown): string {
  const responseMessage = (
    error as { response?: { data?: { message?: string } } }
  )?.response?.data?.message
  if (responseMessage) return responseMessage
  return error instanceof Error ? error.message : ''
}

export function ChannelDebug() {
  const { t } = useTranslation()
  const [channelId, setChannelId] = useState(0)
  const [selectedModel, setSelectedModel] = useState('')
  const [modelFilter, setModelFilter] = useState('')
  const [prompt, setPrompt] = useState(DEFAULT_PROMPT)
  const [messages, setMessages] = useState<ConversationEntry[]>([])
  const [lastResult, setLastResult] = useState<ChannelDebugResult | null>(null)
  const [stream, setStream] = useState(true)
  const [maxTokens, setMaxTokens] = useState(DEFAULT_MAX_TOKENS)

  const channelsQuery = useQuery({
    queryKey: ['channel-debug', 'channels'],
    queryFn: getAllDebugChannels,
    retry: false,
  })
  const channels = useMemo(() => channelsQuery.data ?? [], [channelsQuery.data])
  const selectedChannel = channels.find((channel) => channel.id === channelId)

  useEffect(() => {
    if (channelId || channels.length === 0) return
    const preferred =
      channels.find((channel) => channel.status === 1) ?? channels[0]
    setChannelId(preferred.id)
  }, [channelId, channels])

  const modelsQuery = useQuery({
    queryKey: ['channel-debug', 'models', channelId],
    queryFn: () => getChannelDebugModels(channelId),
    enabled: channelId > 0,
    retry: false,
  })

  const configuredModels = useMemo(
    () => splitModels(selectedChannel?.models),
    [selectedChannel?.models]
  )
  const allModels = useMemo<ModelOption[]>(() => {
    const upstreamModels = modelsQuery.data ?? []
    const configured = new Set(configuredModels)
    const upstream = new Set(upstreamModels)
    return [...new Set([...configuredModels, ...upstreamModels])]
      .map((id) => ({
        id,
        configured: configured.has(id),
        upstream: upstream.has(id),
      }))
      .sort((left, right) => {
        if (left.configured !== right.configured) {
          return left.configured ? -1 : 1
        }
        return left.id.localeCompare(right.id)
      })
  }, [configuredModels, modelsQuery.data])

  const filteredModels = useMemo(() => {
    const filter = modelFilter.trim().toLowerCase()
    if (!filter) return allModels
    return allModels.filter((model) => model.id.toLowerCase().includes(filter))
  }, [allModels, modelFilter])

  useEffect(() => {
    if (!selectedChannel || allModels.length === 0) {
      setSelectedModel('')
      return
    }
    if (allModels.some((model) => model.id === selectedModel)) return
    const preferred = selectedChannel.test_model
      ? allModels.find((model) => model.id === selectedChannel.test_model)
      : undefined
    setSelectedModel(preferred?.id ?? allModels[0].id)
  }, [allModels, selectedChannel, selectedModel])

  const debugMutation = useMutation({
    mutationFn: ({
      activeChannelId,
      request,
    }: {
      activeChannelId: number
      request: ChannelDebugRequest
    }) => runChannelDebug(activeChannelId, request),
    onSuccess: (response) => {
      if (!response.success || !response.data) {
        toast.error(response.message || t('Channel request failed'))
        return
      }
      const content = response.data.content || response.data.raw_response
      setMessages((current) => [
        ...current,
        {
          id: crypto.randomUUID(),
          role: 'assistant',
          content: content || t('Empty response'),
        },
      ])
      setLastResult(response.data)
    },
    onError: (error) => {
      toast.error(requestErrorMessage(error) || t('Channel request failed'))
    },
  })

  const sendMessage = () => {
    const content = prompt.trim()
    if (!content || !channelId || !selectedModel || debugMutation.isPending) {
      return
    }
    const userMessage: ConversationEntry = {
      id: crypto.randomUUID(),
      role: 'user',
      content,
    }
    const nextEntries = [...messages, userMessage]
    const nextMessages: ChannelDebugMessage[] = nextEntries.map(
      ({ role, content: messageContent }) => ({
        role,
        content: messageContent,
      })
    )
    setMessages(nextEntries)
    setPrompt('')
    debugMutation.mutate({
      activeChannelId: channelId,
      request: {
        model: selectedModel,
        messages: nextMessages,
        stream,
        max_tokens: maxTokens,
      },
    })
  }

  const clearConversation = () => {
    setMessages([])
    setLastResult(null)
    setPrompt(DEFAULT_PROMPT)
    toast.success(t('Context cleared'))
  }

  const copyResponse = async () => {
    if (!lastResult) return
    await navigator.clipboard.writeText(
      lastResult.content || lastResult.raw_response
    )
    toast.success(t('Copied to clipboard'))
  }

  const lastUsage = lastResult?.usage

  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>
        <span className='inline-flex items-center gap-2'>
          <Gauge className='text-primary size-5' />
          {t('Channel Debugger')}
          <Badge variant='outline' className='text-[10px] font-medium'>
            ADMIN
          </Badge>
        </span>
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='text-muted-foreground hidden items-center gap-2 text-xs sm:flex'>
          <span className='size-2 rounded-full bg-emerald-500' />
          {t('Configured model')}
          <span className='ml-2 size-2 rounded-full bg-amber-500' />
          {t('Upstream only')}
        </div>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='grid h-full min-h-[680px] gap-3 xl:grid-cols-[300px_minmax(460px,1fr)_310px]'>
          <Card className='min-h-0 overflow-hidden'>
            <CardHeader className='gap-1 border-b pb-4'>
              <CardTitle className='flex items-center gap-2 text-sm'>
                <Server className='size-4' />
                {t('Request target')}
              </CardTitle>
              <CardDescription>
                {t('Select a channel, then test any model exposed upstream.')}
              </CardDescription>
            </CardHeader>
            <CardContent className='flex h-[calc(100%-93px)] min-h-0 flex-col gap-4 p-4'>
              <div className='space-y-2'>
                <label className='text-xs font-medium'>{t('Channel')}</label>
                <NativeSelect
                  className='w-full'
                  value={channelId ? String(channelId) : ''}
                  disabled={channelsQuery.isLoading}
                  onChange={(event) => {
                    setChannelId(Number(event.target.value))
                    setSelectedModel('')
                    setModelFilter('')
                  }}
                >
                  {channels.map((channel) => (
                    <NativeSelectOption
                      key={channel.id}
                      value={String(channel.id)}
                    >
                      #{channel.id} · {channel.name}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
                {selectedChannel && (
                  <div className='text-muted-foreground flex flex-wrap items-center gap-2 text-[11px]'>
                    <Badge
                      variant='outline'
                      className={
                        selectedChannel.status === 1
                          ? 'border-emerald-500/30 text-emerald-600 dark:text-emerald-400'
                          : 'border-rose-500/30 text-rose-600 dark:text-rose-400'
                      }
                    >
                      {selectedChannel.status === 1
                        ? t('Enabled')
                        : t('Disabled')}
                    </Badge>
                    <span>{getChannelTypeLabel(selectedChannel.type)}</span>
                    <span>·</span>
                    <span>{selectedChannel.group}</span>
                  </div>
                )}
              </div>

              <Separator />

              <div className='flex min-h-0 flex-1 flex-col gap-2'>
                <div className='flex items-center justify-between'>
                  <label className='text-xs font-medium'>{t('Model')}</label>
                  <Button
                    type='button'
                    variant='ghost'
                    size='icon-sm'
                    onClick={() => void modelsQuery.refetch()}
                    disabled={!channelId || modelsQuery.isFetching}
                  >
                    <RefreshCw
                      className={`size-3.5 ${modelsQuery.isFetching ? 'animate-spin' : ''}`}
                    />
                  </Button>
                </div>
                <div className='relative'>
                  <Search className='text-muted-foreground absolute top-1/2 left-3 size-3.5 -translate-y-1/2' />
                  <Input
                    value={modelFilter}
                    onChange={(event) => setModelFilter(event.target.value)}
                    placeholder={t('Search models')}
                    className='h-9 pl-9 text-xs'
                  />
                </div>
                <div className='text-muted-foreground flex items-center justify-between text-[11px]'>
                  <span>
                    {t('{{count}} upstream models', {
                      count: modelsQuery.data?.length ?? 0,
                    })}
                  </span>
                  <span>
                    {t('{{count}} enabled', { count: configuredModels.length })}
                  </span>
                </div>
                {modelsQuery.isError && (
                  <Alert variant='destructive' className='py-2'>
                    <AlertTitle className='text-xs'>
                      {t('Upstream model discovery failed')}
                    </AlertTitle>
                    <AlertDescription className='text-[11px]'>
                      {t('Showing configured models as a fallback.')}
                    </AlertDescription>
                  </Alert>
                )}
                <div className='min-h-0 flex-1 overflow-y-auto pr-1'>
                  {modelsQuery.isLoading ? (
                    <div className='text-muted-foreground flex items-center justify-center gap-2 py-10 text-xs'>
                      <LoaderCircle className='size-4 animate-spin' />
                      {t('Loading upstream models')}
                    </div>
                  ) : (
                    <ModelList
                      models={filteredModels}
                      selected={selectedModel}
                      onSelect={setSelectedModel}
                    />
                  )}
                </div>
              </div>
            </CardContent>
          </Card>

          <Card className='flex min-h-0 flex-col overflow-hidden'>
            <CardHeader className='flex-row items-center justify-between gap-3 border-b py-3.5'>
              <div>
                <CardTitle className='flex items-center gap-2 text-sm'>
                  <MessagesSquare className='size-4' />
                  {t('Conversation')}
                </CardTitle>
                <CardDescription className='mt-1'>
                  {t('{{count}} messages in current context', {
                    count: messages.length,
                  })}
                </CardDescription>
              </div>
              <Button
                type='button'
                variant='outline'
                size='sm'
                onClick={clearConversation}
                disabled={debugMutation.isPending}
              >
                <Eraser className='size-3.5' />
                {t('Clear')}
              </Button>
            </CardHeader>

            <div className='bg-muted/15 min-h-0 flex-1 overflow-y-auto p-4 sm:p-5'>
              {messages.length === 0 ? (
                <div className='flex h-full min-h-72 flex-col items-center justify-center text-center'>
                  <div className='from-primary/15 to-primary/5 text-primary grid size-14 place-items-center rounded-2xl bg-gradient-to-br shadow-sm'>
                    <Sparkles className='size-6' />
                  </div>
                  <h3 className='mt-4 font-semibold'>{t('Ready to test')}</h3>
                  <p className='text-muted-foreground mt-1 max-w-sm text-sm leading-6'>
                    {t(
                      'Send a prompt to the selected channel. Replies stay in this window as context until you clear them.'
                    )}
                  </p>
                </div>
              ) : (
                <div className='space-y-5'>
                  {messages.map((message) => (
                    <ConversationMessage key={message.id} message={message} />
                  ))}
                  {debugMutation.isPending && (
                    <div className='flex items-center gap-3'>
                      <div className='bg-primary/10 text-primary grid size-8 place-items-center rounded-full'>
                        <Bot className='size-4' />
                      </div>
                      <div className='bg-muted/70 flex items-center gap-2 rounded-2xl rounded-bl-md border px-4 py-3 text-sm'>
                        <LoaderCircle className='size-4 animate-spin' />
                        {t('Waiting for upstream response')}
                      </div>
                    </div>
                  )}
                </div>
              )}
            </div>

            <div className='border-t p-3 sm:p-4'>
              <Textarea
                value={prompt}
                onChange={(event) => setPrompt(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.shiftKey) {
                    event.preventDefault()
                    sendMessage()
                  }
                }}
                placeholder={t('Enter test text')}
                className='min-h-24 resize-none'
                disabled={debugMutation.isPending}
              />
              <div className='mt-3 flex flex-wrap items-center justify-between gap-3'>
                <div className='flex items-center gap-4'>
                  <label className='flex items-center gap-2 text-xs'>
                    <Switch checked={stream} onCheckedChange={setStream} />
                    {t('Streaming')}
                  </label>
                  <label className='text-muted-foreground flex items-center gap-2 text-xs'>
                    {t('Max output')}
                    <Input
                      type='number'
                      min={1}
                      max={4096}
                      value={maxTokens}
                      onChange={(event) =>
                        setMaxTokens(
                          Math.min(
                            4096,
                            Math.max(1, Number(event.target.value) || 1)
                          )
                        )
                      }
                      className='h-8 w-20 text-xs'
                    />
                  </label>
                </div>
                <Button
                  type='button'
                  onClick={sendMessage}
                  disabled={
                    !prompt.trim() ||
                    !channelId ||
                    !selectedModel ||
                    debugMutation.isPending
                  }
                >
                  {debugMutation.isPending ? (
                    <LoaderCircle className='animate-spin' />
                  ) : (
                    <Send />
                  )}
                  {t('Send test')}
                </Button>
              </div>
            </div>
          </Card>

          <div className='min-h-0 space-y-3 overflow-y-auto pr-0.5'>
            <Card>
              <CardHeader className='pb-3'>
                <CardTitle className='flex items-center gap-2 text-sm'>
                  <Zap className='size-4' />
                  {t('Latest metrics')}
                </CardTitle>
                <CardDescription>
                  {lastResult
                    ? `${lastResult.channel.name} · ${lastResult.model.requested}`
                    : t('Metrics appear after the first response.')}
                </CardDescription>
              </CardHeader>
              <CardContent>
                <div className='grid grid-cols-2 gap-2'>
                  <MetricCard
                    icon={Zap}
                    label={t('First response')}
                    value={formatDuration(
                      lastResult?.timing.first_response_ms ?? Number.NaN
                    )}
                    accent='bg-sky-500/10 text-sky-600 dark:text-sky-400'
                  />
                  <MetricCard
                    icon={Clock3}
                    label={t('Total time')}
                    value={formatDuration(
                      lastResult?.timing.total_ms ?? Number.NaN
                    )}
                    accent='bg-violet-500/10 text-violet-600 dark:text-violet-400'
                  />
                  <MetricCard
                    icon={DatabaseZap}
                    label={t('Total tokens')}
                    value={formatTokens(lastUsage?.total_tokens ?? 0)}
                    accent='bg-emerald-500/10 text-emerald-600 dark:text-emerald-400'
                  />
                  <MetricCard
                    icon={CircleDollarSign}
                    label={t('Test fee')}
                    value={
                      lastResult
                        ? formatLogQuota(lastResult.billing.quota)
                        : '-'
                    }
                    accent='bg-amber-500/10 text-amber-600 dark:text-amber-400'
                  />
                </div>

                <Separator className='my-4' />

                <div className='space-y-2.5 text-xs'>
                  {[
                    [
                      t('Prompt tokens'),
                      formatTokens(lastUsage?.prompt_tokens ?? 0),
                    ],
                    [
                      t('Completion tokens'),
                      formatTokens(lastUsage?.completion_tokens ?? 0),
                    ],
                    [
                      t('Cache hit'),
                      formatTokens(lastUsage?.cached_tokens ?? 0),
                    ],
                    [
                      t('Cache write'),
                      formatTokens(lastUsage?.cache_creation_tokens ?? 0),
                    ],
                    [
                      t('Reasoning tokens'),
                      formatTokens(lastUsage?.reasoning_tokens ?? 0),
                    ],
                  ].map(([label, value]) => (
                    <div
                      key={label}
                      className='flex items-center justify-between gap-3'
                    >
                      <span className='text-muted-foreground'>{label}</span>
                      <span className='font-mono font-medium'>{value}</span>
                    </div>
                  ))}
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className='pb-3'>
                <CardTitle className='flex items-center gap-2 text-sm'>
                  <TimerReset className='size-4' />
                  {t('Request details')}
                </CardTitle>
              </CardHeader>
              <CardContent className='space-y-3 text-xs'>
                <div className='flex items-center justify-between gap-3'>
                  <span className='text-muted-foreground'>{t('Protocol')}</span>
                  <Badge variant='secondary'>
                    {lastResult?.endpoint_type || 'auto'}
                  </Badge>
                </div>
                <div className='flex items-center justify-between gap-3'>
                  <span className='text-muted-foreground'>
                    {t('Upstream model')}
                  </span>
                  <span className='max-w-40 truncate font-mono'>
                    {lastResult?.model.upstream || '-'}
                  </span>
                </div>
                <div className='flex items-center justify-between gap-3'>
                  <span className='text-muted-foreground'>{t('Mapping')}</span>
                  <span>
                    {lastResult?.model.mapped ? t('Applied') : t('Not applied')}
                  </span>
                </div>
                <div className='flex items-center justify-between gap-3'>
                  <span className='text-muted-foreground'>{t('Mode')}</span>
                  <span>
                    {lastResult?.stream ? t('Streaming') : t('Standard')}
                  </span>
                </div>
              </CardContent>
            </Card>

            <Card>
              <CardHeader className='flex-row items-center justify-between gap-3 pb-3'>
                <div>
                  <CardTitle className='text-sm'>
                    {t('Response result')}
                  </CardTitle>
                  <CardDescription>
                    {t('Latest assistant output')}
                  </CardDescription>
                </div>
                <Button
                  type='button'
                  variant='ghost'
                  size='icon-sm'
                  onClick={() => void copyResponse()}
                  disabled={!lastResult}
                >
                  <Copy className='size-3.5' />
                </Button>
              </CardHeader>
              <CardContent>
                <div className='bg-muted/50 max-h-44 min-h-24 overflow-y-auto rounded-lg border p-3 text-xs leading-5 whitespace-pre-wrap'>
                  {lastResult?.content || t('No response yet')}
                </div>
                {lastResult?.raw_response && (
                  <details className='mt-3'>
                    <summary className='text-muted-foreground cursor-pointer text-xs'>
                      {t('View raw response')}
                    </summary>
                    <pre className='bg-muted/50 mt-2 max-h-56 overflow-auto rounded-lg border p-3 text-[10px] leading-4 whitespace-pre-wrap'>
                      {lastResult.raw_response}
                    </pre>
                  </details>
                )}
              </CardContent>
            </Card>
          </div>
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

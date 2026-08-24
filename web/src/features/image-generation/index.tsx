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
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  Clock3,
  Copy,
  Download,
  ExternalLink,
  ImageIcon,
  ImagePlus,
  LoaderCircle,
  Sparkles,
  X,
} from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { Main } from '@/components/layout'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
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
import { Textarea } from '@/components/ui/textarea'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import {
  GENERATION_HISTORY_LIMIT,
  GENERATION_PENDING_TTL_MS,
  readGenerationHistory,
  readGenerationRecord,
  removeGenerationRecord,
} from '../generation-storage'
import {
  createImageTracked,
  getImageModels,
  type GeneratedImage,
  type ImageHistoryEntry,
  type ImageGenerationInput,
  type ImageGenerationRequest,
  type TrackedImageResult,
  type TrackedImageRequest,
} from './api'
import { buildImageMcpPrompt, imageMcpEndpoint } from './mcp-prompt'
import { loadImageGenerationHistory, revokeImageObjectUrls } from './storage'

const imageFormSchema = z.object({
  model: z.string().trim().min(1, 'Model is required'),
  prompt: z.string().trim().min(1, 'Prompt is required').max(32000),
  n: z.number().int().min(1).max(4),
  size: z.string(),
  quality: z.string(),
})

type ImageFormValues = z.infer<typeof imageFormSchema>

const MAX_REFERENCE_IMAGES = 4
const MAX_REFERENCE_IMAGE_SIZE = 10 * 1024 * 1024
const REFERENCE_IMAGE_TYPES = new Set(['image/jpeg', 'image/png', 'image/webp'])

function isSupportedReferenceImage(file: File): boolean {
  return REFERENCE_IMAGE_TYPES.has(file.type.toLowerCase())
}

const imageRenderKeys = new WeakMap<GeneratedImage, string>()
let imageRenderKeySequence = 0

function getImageRenderKey(image: GeneratedImage, scope: string): string {
  let key = imageRenderKeys.get(image)
  if (!key) {
    imageRenderKeySequence += 1
    key = String(imageRenderKeySequence)
    imageRenderKeys.set(image, key)
  }
  return `${scope}:${key}`
}

function getImageSources(image: GeneratedImage): string[] {
  const sources: string[] = []
  // Prefer reusable image bytes over a short-lived upstream URL. This also
  // prevents browser extensions from blocking provider content endpoints.
  if (image.b64_json) sources.push(`data:image/png;base64,${image.b64_json}`)
  if (image.url) sources.push(image.url)
  return sources
}

function detectImageMime(bytes: Uint8Array): string {
  if (
    bytes.length >= 8 &&
    bytes[0] === 0x89 &&
    bytes[1] === 0x50 &&
    bytes[2] === 0x4e &&
    bytes[3] === 0x47
  ) {
    return 'image/png'
  }
  if (
    bytes.length >= 3 &&
    bytes[0] === 0xff &&
    bytes[1] === 0xd8 &&
    bytes[2] === 0xff
  ) {
    return 'image/jpeg'
  }
  if (
    bytes.length >= 12 &&
    String.fromCharCode(...bytes.slice(0, 4)) === 'RIFF' &&
    String.fromCharCode(...bytes.slice(8, 12)) === 'WEBP'
  ) {
    return 'image/webp'
  }
  return 'image/png'
}

function decodeEmbeddedImage(image: GeneratedImage): Blob | null {
  const value =
    image.b64_json || image.url?.startsWith('data:')
      ? image.b64_json || image.url
      : undefined
  if (!value) return null

  const commaIndex = value.indexOf(',')
  const header = commaIndex >= 0 ? value.slice(0, commaIndex) : ''
  const payload = commaIndex >= 0 ? value.slice(commaIndex + 1) : value
  const binary = window.atob(payload.replaceAll(/\s/g, ''))
  const bytes = new Uint8Array(binary.length)
  for (let index = 0; index < binary.length; index += 1) {
    bytes[index] = binary.charCodeAt(index)
  }
  const mimeType = /^data:([^;]+)/.exec(header)?.[1] || detectImageMime(bytes)
  return new Blob([bytes], { type: mimeType })
}

async function resolveImageBlob(
  image: GeneratedImage,
  source: string
): Promise<Blob> {
  const embedded = decodeEmbeddedImage(image)
  if (embedded) return embedded
  if (!source) throw new Error('No image source')

  const response = await fetch(source)
  if (!response.ok) throw new Error(`Image request failed: ${response.status}`)
  const blob = await response.blob()
  if (!blob.type.startsWith('image/')) throw new Error('Not an image')
  return blob
}

function imageExtension(mimeType: string): string {
  if (mimeType === 'image/jpeg') return 'jpg'
  if (mimeType === 'image/webp') return 'webp'
  if (mimeType === 'image/gif') return 'gif'
  return 'png'
}

async function toClipboardPng(blob: Blob): Promise<Blob> {
  if (blob.type === 'image/png') return blob
  const bitmap = await createImageBitmap(blob)
  try {
    const canvas = document.createElement('canvas')
    canvas.width = bitmap.width
    canvas.height = bitmap.height
    const context = canvas.getContext('2d')
    if (!context) throw new Error('Canvas is unavailable')
    context.drawImage(bitmap, 0, 0)
    return await new Promise<Blob>((resolve, reject) => {
      canvas.toBlob(
        (converted) =>
          converted
            ? resolve(converted)
            : reject(new Error('PNG conversion failed')),
        'image/png'
      )
    })
  } finally {
    bitmap.close()
  }
}

type ImagePreviewProps = {
  image: GeneratedImage
  alt: string
  compact?: boolean
  fileName?: string
}

function ImagePreview({
  image,
  alt,
  compact = false,
  fileName = 'generated-image',
}: ImagePreviewProps) {
  const { t } = useTranslation()
  const [localSource, setLocalSource] = useState('')
  const sources = localSource
    ? [
        localSource,
        ...getImageSources(image).filter((item) => item !== image.url),
      ]
    : getImageSources(image)
  const [sourceIndex, setSourceIndex] = useState(0)
  const [failed, setFailed] = useState(false)
  const [action, setAction] = useState<'copy' | 'save' | null>(null)

  useEffect(() => {
    setSourceIndex(0)
    setFailed(false)
    setLocalSource('')
    try {
      const blob = decodeEmbeddedImage(image)
      if (!blob) return
      const objectUrl = window.URL.createObjectURL(blob)
      setLocalSource(objectUrl)
      return () => window.URL.revokeObjectURL(objectUrl)
    } catch {
      // The regular URL source remains available when embedded data is invalid.
    }
  }, [image])

  const source = sources[sourceIndex] ?? ''
  const hasSource = sources.length > 0

  const copyImage = async () => {
    if (!navigator.clipboard?.write || typeof ClipboardItem === 'undefined') {
      toast.error(t('This browser does not support copying images'))
      return
    }
    setAction('copy')
    try {
      const blob = await resolveImageBlob(image, source)
      const clipboardBlob = await toClipboardPng(blob)
      await navigator.clipboard.write([
        new ClipboardItem({ 'image/png': clipboardBlob }),
      ])
      toast.success(t('Image copied to clipboard'))
    } catch {
      toast.error(t('Could not copy image'))
    } finally {
      setAction(null)
    }
  }

  const saveImage = async () => {
    setAction('save')
    try {
      const blob = await resolveImageBlob(image, source)
      const objectUrl = window.URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = objectUrl
      link.download = `${fileName}.${imageExtension(blob.type)}`
      document.body.append(link)
      link.click()
      link.remove()
      window.setTimeout(() => window.URL.revokeObjectURL(objectUrl), 1000)
      toast.success(t('Image saved'))
    } catch {
      toast.error(t('Could not save image'))
    } finally {
      setAction(null)
    }
  }

  return (
    <div className='space-y-2'>
      {source && !failed ? (
        <img
          key={source}
          className={`bg-muted w-full rounded-md object-contain ${compact ? 'aspect-square' : 'max-h-[32rem]'}`}
          src={source}
          alt={alt}
          onError={() => {
            if (sourceIndex + 1 < sources.length) {
              setSourceIndex((index) => index + 1)
            } else {
              setFailed(true)
            }
          }}
        />
      ) : (
        <div className='bg-muted text-muted-foreground flex aspect-square items-center justify-center rounded-md p-4 text-center text-sm'>
          {hasSource
            ? t(
                'Image could not be loaded because the upstream link is unavailable, expired, blocked by cross-origin policy, or returned an error'
              )
            : t('No image data returned')}
        </div>
      )}
      {source && !failed && (
        <div
          className={`grid gap-2 ${compact ? 'grid-cols-1' : 'grid-cols-3'}`}
        >
          <Button
            size={compact ? 'sm' : undefined}
            variant='outline'
            render={<a href={source} target='_blank' rel='noreferrer' />}
          >
            <ExternalLink />
            {t('Open image')}
          </Button>
          <Button
            type='button'
            size={compact ? 'sm' : undefined}
            variant='outline'
            disabled={action !== null}
            onClick={() => void copyImage()}
          >
            {action === 'copy' ? (
              <LoaderCircle className='animate-spin' />
            ) : (
              <Copy />
            )}
            {t('Copy image')}
          </Button>
          <Button
            type='button'
            size={compact ? 'sm' : undefined}
            variant='outline'
            disabled={action !== null}
            onClick={() => void saveImage()}
          >
            {action === 'save' ? (
              <LoaderCircle className='animate-spin' />
            ) : (
              <Download />
            )}
            {t('Save image')}
          </Button>
        </div>
      )}
    </div>
  )
}

function imageRequestErrorMessage(error: unknown): string {
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

export function ImageGeneration() {
  const { t } = useTranslation()
  const { copyToClipboard } = useCopyToClipboard({
    successMessage: t('MCP prompt copied'),
  })
  const storedObjectUrls = useRef<string[]>([])
  const componentMounted = useRef(true)
  const [images, setImages] = useState<GeneratedImage[]>(() => {
    return (
      readGenerationRecord<TrackedImageResult>('image-latest')?.value.images ??
      []
    )
  })
  const [imagesRestored, setImagesRestored] = useState(() =>
    Boolean(readGenerationRecord<TrackedImageResult>('image-latest'))
  )
  const [referenceImages, setReferenceImages] = useState<File[]>([])
  const [imageHistory, setImageHistory] = useState<ImageHistoryEntry[]>(() =>
    readGenerationHistory<ImageHistoryEntry>('image-history')
  )
  const [pendingAfterReload, setPendingAfterReload] = useState(() => {
    const pending = readGenerationRecord<TrackedImageRequest>(
      'image-pending',
      GENERATION_PENDING_TTL_MS
    )
    const result = readGenerationRecord<TrackedImageResult>('image-latest')
    return Boolean(pending && result?.value.id !== pending.value.id)
  })

  const refreshStoredImages = useCallback(async (restoreLatest: boolean) => {
    try {
      const stored = await loadImageGenerationHistory()
      if (!componentMounted.current) {
        revokeImageObjectUrls(stored.objectUrls)
        return
      }
      revokeImageObjectUrls(storedObjectUrls.current)
      storedObjectUrls.current = stored.objectUrls
      const fallbackEntries =
        readGenerationHistory<ImageHistoryEntry>('image-history')
      const storedIds = new Set(stored.entries.map((entry) => entry.id))
      const mergedEntries = [
        ...stored.entries,
        ...fallbackEntries.filter((entry) => !storedIds.has(entry.id)),
      ]
        .sort((left, right) => right.createdAt - left.createdAt)
        .slice(0, GENERATION_HISTORY_LIMIT)
      if (mergedEntries.length > 0) {
        setImageHistory(mergedEntries)
        if (restoreLatest) {
          setImages(mergedEntries[0].images)
          setImagesRestored(true)
        }
      }
    } catch {
      // IndexedDB can be unavailable in private browsing. URL-only legacy
      // localStorage records remain visible through the initial state.
    }
  }, [])

  useEffect(() => {
    componentMounted.current = true
    void refreshStoredImages(true)
    return () => {
      componentMounted.current = false
      revokeImageObjectUrls(storedObjectUrls.current)
      storedObjectUrls.current = []
    }
  }, [refreshStoredImages])

  const form = useForm<ImageFormValues>({
    resolver: zodResolver(imageFormSchema),
    defaultValues: {
      model: '',
      prompt: '',
      n: 1,
      size: 'auto',
      quality: 'auto',
    },
  })

  const modelsQuery = useQuery({
    queryKey: ['image-generation-models'],
    queryFn: getImageModels,
    retry: false,
  })
  let modelDescription = t(
    'No available image models are configured in Channels'
  )
  if (modelsQuery.isLoading) {
    modelDescription = t('Loading models from Channels')
  } else if (modelsQuery.data?.length) {
    modelDescription = t('{{count}} available image models', {
      count: modelsQuery.data.length,
    })
  }

  useEffect(() => {
    const firstModel = modelsQuery.data?.[0]
    if (!firstModel) return
    const currentModel = form.getValues('model')
    if (!modelsQuery.data?.includes(currentModel)) {
      form.setValue('model', firstModel)
    }
  }, [form, modelsQuery.data])

  const createMutation = useMutation({
    mutationFn: createImageTracked,
    onSuccess: (response) => {
      setImages(response.data ?? [])
      setImageHistory(readGenerationHistory<ImageHistoryEntry>('image-history'))
      void refreshStoredImages(false)
      setImagesRestored(false)
      setPendingAfterReload(false)
      removeGenerationRecord('image-pending')
      toast.success(t('Image generated successfully'))
    },
    onError: (error) => {
      setPendingAfterReload(false)
      removeGenerationRecord('image-pending')
      toast.error(
        imageRequestErrorMessage(error) || t('Image generation request failed')
      )
    },
  })

  // If the route was changed while the synchronous request was still running,
  // createImageTracked writes the result after it resolves. Pick that result
  // up when the user returns to this page.
  useEffect(() => {
    if (!pendingAfterReload) return

    const refreshStoredResult = () => {
      const result = readGenerationRecord<TrackedImageResult>('image-latest')
      const pending = readGenerationRecord<TrackedImageRequest>(
        'image-pending',
        GENERATION_PENDING_TTL_MS
      )

      if (result && (!pending || result.value.id === pending.value.id)) {
        void refreshStoredImages(true)
      }
      if (!pending || (result && result.value.id === pending.value.id)) {
        setPendingAfterReload(false)
      }
    }

    refreshStoredResult()
    const interval = window.setInterval(refreshStoredResult, 1000)
    return () => window.clearInterval(interval)
  }, [pendingAfterReload, refreshStoredImages])

  useEffect(() => {
    const isGenerating = createMutation.isPending || pendingAfterReload
    if (!isGenerating) return

    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', handleBeforeUnload)
    return () => window.removeEventListener('beforeunload', handleBeforeUnload)
  }, [createMutation.isPending, pendingAfterReload])

  const onSubmit = (values: ImageFormValues) => {
    if (createMutation.isPending || pendingAfterReload) {
      toast.error(
        t(
          'Wait for the current image request to finish before submitting another'
        )
      )
      return
    }
    const request: ImageGenerationRequest = {
      model: values.model.trim(),
      prompt: values.prompt.trim(),
      n: values.n,
      response_format: 'b64_json',
    }
    if (values.size !== 'auto') request.size = values.size
    if (values.quality !== 'auto') request.quality = values.quality
    setPendingAfterReload(true)
    setImagesRestored(false)
    const input: ImageGenerationInput = {
      request,
      referenceImages,
    }
    createMutation.mutate(input)
  }

  const copyMcpPrompt = () => {
    const origin =
      typeof window !== 'undefined' ? window.location.origin : undefined
    void copyToClipboard(
      buildImageMcpPrompt(
        form.getValues(),
        referenceImages,
        imageMcpEndpoint(origin)
      )
    )
  }

  return (
    <Main className='space-y-6 overflow-x-hidden overflow-y-auto px-3 py-6 pb-12 sm:px-4'>
      <div>
        <h1 className='flex items-center gap-2 text-2xl font-semibold'>
          <ImageIcon className='size-6' />
          {t('Image Generation')}
        </h1>
        <p className='text-muted-foreground mt-1 text-sm'>
          {t('Generate images with configured image models')}
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
              {t('Images are generated by your configured upstream channels')}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <Form {...form}>
              <form
                className='space-y-5'
                onSubmit={form.handleSubmit(onSubmit)}
              >
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
                          onChange={field.onChange}
                        >
                          {(modelsQuery.data ?? []).map((model) => (
                            <NativeSelectOption key={model} value={model}>
                              {model}
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
                  name='prompt'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Prompt')}</FormLabel>
                      <FormControl>
                        <Textarea
                          {...field}
                          className='min-h-40 resize-y'
                          placeholder={t(
                            'Describe the image, composition, lighting, and style'
                          )}
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <div className='space-y-3'>
                  <div className='space-y-1'>
                    <label
                      className='text-sm font-medium'
                      htmlFor='image-reference-files'
                    >
                      {t('Reference images (optional)')}
                    </label>
                    <Input
                      id='image-reference-files'
                      type='file'
                      accept='image/jpeg,image/png,image/webp'
                      multiple
                      disabled={createMutation.isPending || pendingAfterReload}
                      onChange={(event) => {
                        const selected = [...(event.currentTarget.files ?? [])]
                        event.currentTarget.value = ''
                        if (selected.length === 0) return
                        const next = [...referenceImages, ...selected]
                        if (next.length > MAX_REFERENCE_IMAGES) {
                          toast.error(
                            t('Select no more than {{count}} reference images', {
                              count: MAX_REFERENCE_IMAGES,
                            })
                          )
                          return
                        }
                        if (!selected.every(isSupportedReferenceImage)) {
                          toast.error(
                            t('Only JPG, PNG, and WEBP images are supported')
                          )
                          return
                        }
                        if (
                          selected.some(
                            (file) => file.size > MAX_REFERENCE_IMAGE_SIZE
                          )
                        ) {
                          toast.error(
                            t('Each reference image must not exceed 10 MiB')
                          )
                          return
                        }
                        setReferenceImages(next)
                      }}
                    />
                    <p className='text-muted-foreground text-sm'>
                      {t(
                        'Upload up to {{count}} static JPG, PNG, or WEBP images, up to 10 MiB each.',
                        { count: MAX_REFERENCE_IMAGES }
                      )}
                    </p>
                  </div>

                  {referenceImages.length > 0 && (
                    <div className='space-y-2 rounded-md border p-3'>
                      {referenceImages.map((file, index) => (
                        <div
                          key={`${file.name}-${file.size}-${file.lastModified}`}
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
                            disabled={
                              createMutation.isPending || pendingAfterReload
                            }
                            onClick={() =>
                              setReferenceImages((files) =>
                                files.filter(
                                  (_, fileIndex) => fileIndex !== index
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

                  {referenceImages.length > 0 && (
                    <Alert>
                      <ImagePlus />
                      <AlertDescription>
                        {t(
                          'Reference images use the configured upstream image-edit API; support depends on the selected model and channel'
                        )}
                      </AlertDescription>
                    </Alert>
                  )}
                </div>

                <div className='grid gap-4 md:grid-cols-3'>
                  <FormField
                    control={form.control}
                    name='size'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Size')}</FormLabel>
                        <FormControl>
                          <NativeSelect
                            className='w-full'
                            value={field.value}
                            onChange={field.onChange}
                          >
                            {[
                              'auto',
                              '1024x1024',
                              '1024x1536',
                              '1536x1024',
                              '1024x1792',
                              '1792x1024',
                              '512x512',
                            ].map((size) => (
                              <NativeSelectOption key={size} value={size}>
                                {size === 'auto' ? t('Automatic') : size}
                              </NativeSelectOption>
                            ))}
                          </NativeSelect>
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='quality'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Quality')}</FormLabel>
                        <FormControl>
                          <NativeSelect
                            className='w-full'
                            value={field.value}
                            onChange={field.onChange}
                          >
                            {[
                              'auto',
                              'standard',
                              'hd',
                              'low',
                              'medium',
                              'high',
                            ].map((quality) => (
                              <NativeSelectOption key={quality} value={quality}>
                                {t(quality)}
                              </NativeSelectOption>
                            ))}
                          </NativeSelect>
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />

                  <FormField
                    control={form.control}
                    name='n'
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>{t('Image count')}</FormLabel>
                        <FormControl>
                          <Input
                            type='number'
                            min={1}
                            max={4}
                            value={field.value}
                            onChange={(event) =>
                              field.onChange(event.target.valueAsNumber)
                            }
                          />
                        </FormControl>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                </div>

                <Alert>
                  <Sparkles />
                  <AlertTitle>{t('Lightweight relay')}</AlertTitle>
                  <AlertDescription>
                    {t(
                      'Generated images are returned directly from the upstream and are not stored on this server'
                    )}
                  </AlertDescription>
                </Alert>

                {(createMutation.isPending || pendingAfterReload) && (
                  <Alert>
                    <Clock3 />
                    <AlertTitle>
                      {t('Image generation is still in progress')}
                    </AlertTitle>
                    <AlertDescription>
                      {t(
                        'Switching sections is supported, but refreshing or closing the browser may prevent a synchronous image result from being recovered'
                      )}
                    </AlertDescription>
                  </Alert>
                )}

                {!createMutation.isPending &&
                  !pendingAfterReload &&
                  imagesRestored && (
                    <Alert>
                      <ImageIcon />
                      <AlertDescription>
                        {t(
                          'This result was restored from this browser and will expire after 24 hours'
                        )}
                      </AlertDescription>
                    </Alert>
                  )}

                <Button
                  className='w-full'
                  type='submit'
                  disabled={
                    createMutation.isPending ||
                    pendingAfterReload ||
                    (modelsQuery.data?.length ?? 0) === 0
                  }
                >
                  {createMutation.isPending ? (
                    <LoaderCircle className='animate-spin' />
                  ) : (
                    <Sparkles />
                  )}
                  {t('Generate image')}
                </Button>
              </form>
            </Form>
          </CardContent>
        </Card>

        <div className='space-y-6'>
          <Card className='min-h-96'>
            <CardHeader>
              <CardTitle>{t('Generated images')}</CardTitle>
              <CardDescription>
                {t('Results from the latest request appear here')}
              </CardDescription>
            </CardHeader>
            <CardContent>
              {images.length === 0 ? (
                <div className='text-muted-foreground flex min-h-72 flex-col items-center justify-center gap-3 text-center'>
                  <ImageIcon className='size-12 opacity-40' />
                  <p>{t('Submit a request to see generated images here')}</p>
                </div>
              ) : (
                <div className='max-h-[70vh] space-y-4 overflow-y-auto pr-1'>
                  {images.map((image, index) => {
                    return (
                      <div
                        key={getImageRenderKey(image, 'latest')}
                        className='space-y-3 rounded-lg border p-3'
                      >
                        <ImagePreview
                          image={image}
                          alt={image.revised_prompt || t('Generated image')}
                          fileName={`generated-image-${index + 1}`}
                        />
                        {image.revised_prompt && (
                          <p className='text-muted-foreground text-xs'>
                            {image.revised_prompt}
                          </p>
                        )}
                      </div>
                    )
                  })}
                </div>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>{t('Recent image generations')}</CardTitle>
              <CardDescription>
                {t(
                  'Recent image results are kept in this browser for up to 24 hours'
                )}
              </CardDescription>
            </CardHeader>
            <CardContent className='max-h-[38rem] overflow-y-auto'>
              {imageHistory.length === 0 ? (
                <p className='text-muted-foreground py-8 text-center text-sm'>
                  {t('No image generations yet')}
                </p>
              ) : (
                <div className='space-y-4'>
                  {imageHistory.map((entry) => {
                    const imagesWithSources = entry.images.filter(
                      (image) => getImageSources(image).length > 0
                    )
                    return (
                      <div
                        key={entry.id}
                        className='space-y-3 rounded-lg border p-3'
                      >
                        <div className='flex items-center justify-between gap-2'>
                          <span className='truncate text-sm font-medium'>
                            {entry.model}
                          </span>
                          <span className='text-muted-foreground shrink-0 text-xs'>
                            {new Date(entry.createdAt).toLocaleString()}
                          </span>
                        </div>
                        <p className='text-muted-foreground line-clamp-3 text-sm'>
                          {entry.prompt}
                        </p>
                        {imagesWithSources.length > 0 ? (
                          <div className='grid gap-2 sm:grid-cols-2'>
                            {imagesWithSources.map((image, index) => (
                              <div
                                key={getImageRenderKey(image, entry.id)}
                                className='space-y-2'
                              >
                                <ImagePreview
                                  image={image}
                                  alt={
                                    image.revised_prompt || t('Generated image')
                                  }
                                  compact
                                  fileName={`generated-image-${entry.id}-${index + 1}`}
                                />
                              </div>
                            ))}
                          </div>
                        ) : (
                          <p className='text-muted-foreground bg-muted rounded-md p-3 text-xs'>
                            {t(
                              'The upstream did not return reusable image data for this result'
                            )}
                          </p>
                        )}
                        <Button
                          className='w-full'
                          size='sm'
                          variant='secondary'
                          disabled={imagesWithSources.length === 0}
                          onClick={() => {
                            setImages(entry.images)
                            setImagesRestored(true)
                          }}
                        >
                          {t('View')}
                        </Button>
                      </div>
                    )
                  })}
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      </div>
    </Main>
  )
}

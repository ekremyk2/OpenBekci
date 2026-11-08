import { useRef, useState } from 'react'
import { useLingui } from '@lingui/react/macro'
import { BoldIcon, HeadingIcon, ItalicIcon, LinkIcon, ListIcon } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Textarea } from '@/components/ui/textarea'
import MarkdownView from './Markdown'

/** Markdown source with a toolbar and a live, sanitized preview. Loaded lazily. */
export default function MarkdownEditor({
  id,
  value,
  onChange,
  invalid,
}: {
  id: string
  value: string
  onChange: (next: string) => void
  invalid?: boolean
}) {
  const { t } = useLingui()
  const [tab, setTab] = useState<'write' | 'preview'>('write')
  const ref = useRef<HTMLTextAreaElement>(null)

  /** Wraps the selection, or inserts `before + placeholder + after` at the caret. */
  function wrap(before: string, after = before, placeholder = '') {
    const el = ref.current
    const start = el?.selectionStart ?? value.length
    const end = el?.selectionEnd ?? value.length
    const selected = value.slice(start, end) || placeholder
    onChange(value.slice(0, start) + before + selected + after + value.slice(end))
    requestAnimationFrame(() => {
      el?.focus()
      el?.setSelectionRange(start + before.length, start + before.length + selected.length)
    })
  }
  function linePrefix(prefix: string) {
    const el = ref.current
    const start = el?.selectionStart ?? 0
    const lineStart = value.lastIndexOf('\n', start - 1) + 1
    onChange(value.slice(0, lineStart) + prefix + value.slice(lineStart))
  }

  return (
    <Tabs value={tab} onValueChange={(next) => setTab(next as 'write' | 'preview')}>
      <div className="flex flex-wrap items-center gap-1 border-b">
        <TabsList
          aria-label={t`Editor mode`}
          className="h-auto gap-1 rounded-none bg-transparent p-0"
        >
          {(['write', 'preview'] as const).map((id) => (
            <TabsTrigger
              key={id}
              value={id}
              className="-mb-px h-auto flex-none rounded-none border-b-2 border-transparent px-3 py-1.5 data-[state=active]:border-primary data-[state=active]:bg-transparent data-[state=active]:shadow-none"
            >
              {id === 'write' ? t`Write` : t`Preview`}
            </TabsTrigger>
          ))}
        </TabsList>
        {tab === 'write' ? (
          <div className="ms-auto flex gap-1">
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t`Bold`}
              onClick={() => wrap('**', '**', t`bold text`)}
            >
              <BoldIcon />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t`Italic`}
              onClick={() => wrap('_', '_', t`italic text`)}
            >
              <ItalicIcon />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t`Heading`}
              onClick={() => linePrefix('## ')}
            >
              <HeadingIcon />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t`List`}
              onClick={() => linePrefix('- ')}
            >
              <ListIcon />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              aria-label={t`Link`}
              onClick={() => wrap('[', '](https://)', t`link text`)}
            >
              <LinkIcon />
            </Button>
          </div>
        ) : null}
      </div>
      <TabsContent value="write">
        <Textarea
          id={id}
          ref={ref}
          rows={10}
          value={value}
          aria-invalid={invalid}
          onChange={(e) => onChange(e.target.value)}
        />
      </TabsContent>
      <TabsContent value="preview" className="min-h-40 rounded-md border p-3">
        {value.trim() ? (
          <MarkdownView source={value} />
        ) : (
          <p className="text-sm text-muted-foreground">{t`Nothing to preview yet.`}</p>
        )}
      </TabsContent>
    </Tabs>
  )
}

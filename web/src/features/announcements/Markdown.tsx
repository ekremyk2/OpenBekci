import ReactMarkdown from 'react-markdown'

/*
 * Sanitized Markdown rendering. react-markdown builds React elements (no innerHTML), `skipHtml`
 * drops any raw HTML in the source, images are not rendered (attachments carry pictures) and link
 * protocols are filtered by its default `urlTransform`. Loaded lazily: it is only needed on the
 * announcement screens.
 */
export default function MarkdownView({ source }: { source: string }) {
  return (
    <div className="flex flex-col gap-3 text-sm leading-relaxed [&_a]:text-primary [&_a]:underline [&_h1]:text-xl [&_h1]:font-semibold [&_h2]:text-lg [&_h2]:font-semibold [&_h3]:font-semibold [&_ol]:list-decimal [&_ol]:ps-6 [&_ul]:list-disc [&_ul]:ps-6">
      <ReactMarkdown
        skipHtml
        disallowedElements={['img']}
        components={{
          // a blocked protocol (javascript:, data:, ...) leaves no href: show the text, not a link
          // (an empty href would reload this page in a new tab)
          a: ({ href, children }) =>
            href ? (
              <a href={href} target="_blank" rel="noopener noreferrer nofollow">
                {children}
              </a>
            ) : (
              <span>{children}</span>
            ),
        }}
      >
        {source}
      </ReactMarkdown>
    </div>
  )
}

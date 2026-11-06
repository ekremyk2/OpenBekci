// Forbids physical Tailwind utilities in string literals so layouts stay RTL-ready (web plan §3/§10).
// eslint-plugin-tailwindcss does not support Tailwind v4, hence this small rule.

const PHYSICAL = [
  // margin / padding: ml-2, -mr-1, md:pl-4, !pr-2
  [
    /(?<![\w-])-?(m|p)(l|r)-(?=(\d|px\b|auto\b|\[|\())/,
    (m) => `${m[1]}${m[2] === 'l' ? 's' : 'e'}-`,
  ],
  // inset: left-0, -right-2, right-1/2, left-[10px]
  [
    /(?<![\w-])-?(left|right)-(?=(\d|\[|px\b|full\b|auto\b|1\/|2\/|3\/|4\/))/,
    (m) => (m[1] === 'left' ? 'start-' : 'end-'),
  ],
  // text alignment
  [/(?<![\w-])text-(left|right)(?![\w-])/, (m) => (m[1] === 'left' ? 'text-start' : 'text-end')],
  // borders and radii on one physical side
  [
    /(?<![\w-])border-(l|r)(?![a-z])(?=$|[\s\-[:/]|\d)/,
    (m) => (m[1] === 'l' ? 'border-s' : 'border-e'),
  ],
  [
    /(?<![\w-])rounded-(l|r|tl|tr|bl|br)(?![a-z])(?=$|[\s\-[:/]|\d)/,
    (m) => `rounded-${{ l: 's', r: 'e', tl: 'ss', tr: 'se', bl: 'es', br: 'ee' }[m[1]]}`,
  ],
  [/(?<![\w-])float-(left|right)(?![\w-])/, (m) => (m[1] === 'left' ? 'float-start' : 'float-end')],
  [/(?<![\w-])space-x-/, () => 'gap-x-* on a flex/grid parent'],
]

/** @type {import('eslint').Rule.RuleModule} */
export const logicalProperties = {
  meta: {
    type: 'problem',
    docs: {
      description:
        'Use logical Tailwind utilities (ms-, me-, ps-, pe-, start-, end-, text-start/end).',
    },
    schema: [],
    messages: {
      physical:
        'Physical utility "{{found}}" is not RTL-safe; use the logical equivalent ({{suggest}}).',
    },
  },
  create(context) {
    /** @param {import('estree').Node} node @param {string} text */
    function check(node, text) {
      for (const token of text.split(/\s+/)) {
        if (!token) continue
        // strip variants such as md:hover:
        const bare = token.slice(token.lastIndexOf(':') + 1).replace(/^!/, '')
        for (const [re, suggest] of PHYSICAL) {
          const m = re.exec(bare)
          if (m) {
            context.report({
              node,
              messageId: 'physical',
              data: { found: token, suggest: suggest(m) },
            })
            break
          }
        }
      }
    }
    return {
      Literal(node) {
        if (typeof node.value === 'string') check(node, node.value)
      },
      TemplateElement(node) {
        check(node, node.value.cooked ?? node.value.raw)
      },
    }
  },
}

export default { rules: { 'logical-properties': logicalProperties } }

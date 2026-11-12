import type { ReactNode } from 'react'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'

/**
 * Underlined tab bar over one panel (Radix tabs: tablist, tab and tabpanel roles, `aria-controls`,
 * roving tabindex, arrow keys). The panel shows `children` for the selected tab, so pages that
 * switch one list between views (the state lives in the page or the URL) stay correct.
 */
export function TabBar<T extends string>({
  value,
  onValueChange,
  label,
  tabs,
  className,
  panelClassName,
  children,
}: {
  value: T
  onValueChange: (next: T) => void
  label: string
  tabs: readonly { id: T; label: string }[]
  className?: string
  panelClassName?: string
  children: ReactNode
}) {
  return (
    <Tabs
      value={value}
      onValueChange={(next) => onValueChange(next as T)}
      className={cn('gap-4', className)}
    >
      <TabsList
        aria-label={label}
        className="h-auto w-full flex-wrap justify-start gap-1 rounded-none border-b bg-transparent p-0"
      >
        {tabs.map((tab) => (
          <TabsTrigger
            key={tab.id}
            value={tab.id}
            className="-mb-px h-auto min-h-11 flex-none lg:min-h-9 rounded-none border-b-2 border-transparent px-3 py-2 data-[state=active]:border-primary data-[state=active]:bg-transparent data-[state=active]:shadow-none"
          >
            {tab.label}
          </TabsTrigger>
        ))}
      </TabsList>
      <TabsContent value={value} className={cn('flex flex-col gap-4', panelClassName)}>
        {children}
      </TabsContent>
    </Tabs>
  )
}

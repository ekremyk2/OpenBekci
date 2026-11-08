import { createFileRoute } from '@tanstack/react-router'
import { ReportsPage, type ReportId } from '@/features/money/ReportsPage'
import { Gate } from '@/features/money/ui'

const IDS = [
  'income-expense',
  'debtors',
  'budget',
  'trial-balance',
  'aging',
  'collectors',
  'bank',
  'funds',
]

export const Route = createFileRoute('/orgs/$orgId/money/reports')({
  validateSearch: (s: Record<string, unknown>): { report: ReportId } => ({
    report: (IDS.includes(String(s.report)) ? s.report : 'income-expense') as ReportId,
  }),
  component: function ReportsRoute() {
    const { report } = Route.useSearch()
    return (
      <Gate anyOf={['reports.read', 'reports.debtors', 'reports.aggregate']}>
        <ReportsPage report={report} />
      </Gate>
    )
  },
})

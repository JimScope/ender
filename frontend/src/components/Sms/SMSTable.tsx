import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { DataTable } from "@/components/Common/DataTable"
import type { SMSMessage } from "@/types/collections"
import { getColumns } from "./columns"

interface SMSTableProps {
  data: SMSMessage[]
  /** Total messages on the server — shows a truncation notice when more
   * exist than were downloaded. */
  totalCount?: number
}

export function SMSTable({ data, totalCount }: SMSTableProps) {
  const { t } = useTranslation()
  const columns = useMemo(() => getColumns(t), [t])

  const sortedData = useMemo(
    () =>
      [...data].sort(
        (a, b) => new Date(b.created).getTime() - new Date(a.created).getTime(),
      ),
    [data],
  )

  return (
    <DataTable
      columns={columns}
      data={sortedData}
      caption={t("sms.smsMessages")}
      totalCount={totalCount}
    />
  )
}

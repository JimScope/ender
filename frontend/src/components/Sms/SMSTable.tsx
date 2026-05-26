import { useMemo } from "react"
import { useTranslation } from "react-i18next"
import { DataTable } from "@/components/Common/DataTable"
import type { SMSMessage } from "@/types/collections"
import { getColumns } from "./columns"

interface SMSTableProps {
  data: SMSMessage[]
}

export function SMSTable({ data }: SMSTableProps) {
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
    />
  )
}

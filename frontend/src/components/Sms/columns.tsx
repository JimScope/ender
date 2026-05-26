import type { ColumnDef } from "@tanstack/react-table"
import type { TFunction } from "i18next"

import { DeviceCell } from "@/components/Devices/DeviceCell"
import { Badge } from "@/components/ui/badge"
import { formatDate } from "@/lib/utils"
import type { SMSMessage } from "@/types/collections"
import { SMSActionsMenu } from "./SMSActionsMenu"

function statusBadgeVariant(
  status: string,
): "default" | "secondary" | "destructive" | "outline" {
  switch (status) {
    case "delivered":
      return "default"
    case "sent":
      return "secondary"
    case "failed":
      return "destructive"
    default:
      return "outline"
  }
}

function truncate(text: string, maxLength: number): string {
  if (text.length <= maxLength) return text
  return `${text.slice(0, maxLength)}...`
}

export function getColumns(t: TFunction): ColumnDef<SMSMessage>[] {
  return [
    {
      accessorKey: "to",
      header: t("sms.to"),
      cell: ({ row }) => {
        const msg = row.original
        const display =
          msg.message_type === "incoming" ? msg.from_number || msg.to : msg.to
        return <span className="font-medium">{display}</span>
      },
    },
    {
      accessorKey: "device",
      header: t("sms.device"),
      cell: ({ row }) => <DeviceCell device={row.original.expand?.device} />,
    },
    {
      accessorKey: "body",
      header: t("sms.body"),
      cell: ({ row }) => (
        <span className="max-w-xs truncate" title={row.original.body}>
          {truncate(row.original.body, 50)}
        </span>
      ),
    },
    {
      accessorKey: "status",
      header: t("common.status"),
      cell: ({ row }) => (
        <Badge variant={statusBadgeVariant(row.original.status || "pending")}>
          {row.original.status || "pending"}
        </Badge>
      ),
    },
    {
      accessorKey: "message_type",
      header: t("sms.type"),
      cell: ({ row }) => (
        <Badge variant="outline">
          {row.original.message_type || "outgoing"}
        </Badge>
      ),
    },
    {
      accessorKey: "created",
      header: t("sms.date"),
      cell: ({ row }) => (
        <span className="text-muted-foreground text-sm">
          {formatDate(row.original.created)}
        </span>
      ),
    },
    {
      id: "actions",
      header: () => null,
      cell: ({ row }) => <SMSActionsMenu sms={row.original} />,
    },
  ]
}

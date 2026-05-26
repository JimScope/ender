import type { ColumnDef } from "@tanstack/react-table"
import type { TFunction } from "i18next"

import { DeviceCell } from "@/components/Devices/DeviceCell"
import { Badge } from "@/components/ui/badge"
import { cn, formatDate } from "@/lib/utils"
import type { ScheduledSMS } from "@/types/collections"
import { ScheduledSMSActionsMenu } from "./ScheduledSMSActionsMenu"

export const getColumns = (t: TFunction): ColumnDef<ScheduledSMS>[] => [
  {
    accessorKey: "name",
    header: t("common.name"),
    cell: ({ row }) => <span className="font-medium">{row.original.name}</span>,
  },
  {
    accessorKey: "device_id",
    header: t("sms.device"),
    cell: ({ row }) => <DeviceCell device={row.original.expand?.device_id} />,
  },
  {
    accessorKey: "schedule_type",
    header: t("sms.type"),
    cell: ({ row }) => {
      const type = row.original.schedule_type
      return (
        <Badge variant="secondary">
          {type === "one_time"
            ? t("scheduled.oneTime")
            : t("scheduled.recurring")}
        </Badge>
      )
    },
  },
  {
    accessorKey: "recipients",
    header: t("scheduled.recipients"),
    cell: ({ row }) => {
      const recipients = row.original.recipients
      const count = Array.isArray(recipients) ? recipients.length : 0
      return (
        <span className="text-muted-foreground">
          {t("scheduled.recipient", { count })}
        </span>
      )
    },
  },
  {
    accessorKey: "next_run_at",
    header: t("scheduled.nextRun"),
    cell: ({ row }) => (
      <span className="text-muted-foreground">
        {formatDate(row.original.next_run_at)}
      </span>
    ),
  },
  {
    accessorKey: "status",
    header: t("common.status"),
    cell: ({ row }) => {
      const status = row.original.status
      return (
        <div className="flex items-center gap-2">
          <span
            className={cn(
              "size-2 rounded-full",
              status === "active"
                ? "bg-green-500"
                : status === "paused"
                  ? "bg-yellow-500"
                  : "bg-gray-400",
            )}
          />
          <span
            className={
              status === "active" ? "" : "text-muted-foreground capitalize"
            }
          >
            {status === "active"
              ? t("scheduled.statusActive")
              : status === "paused"
                ? t("scheduled.statusPaused")
                : t("scheduled.statusCompleted")}
          </span>
        </div>
      )
    },
  },
  {
    id: "actions",
    header: () => <span className="sr-only">{t("common.actions")}</span>,
    cell: ({ row }) => (
      <div className="flex justify-end">
        <ScheduledSMSActionsMenu schedule={row.original} />
      </div>
    ),
  },
]

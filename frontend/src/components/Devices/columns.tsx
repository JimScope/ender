import type { ColumnDef } from "@tanstack/react-table"
import type { TFunction } from "i18next"
import { Cloud, Smartphone, Usb } from "lucide-react"

import { DeviceActionsMenu } from "@/components/Devices/DeviceActionsMenu"
import { formatDate } from "@/lib/utils"
import type { Device } from "@/types/collections"

export function getColumns(
  t: TFunction,
  modemStatus?: Record<string, boolean>,
): ColumnDef<Device>[] {
  return [
    {
      accessorKey: "name",
      header: t("common.name"),
      cell: ({ row }) => (
        <span className="font-medium">{row.original.name}</span>
      ),
    },
    {
      accessorKey: "device_type",
      header: t("devices.type"),
      cell: ({ row }) => {
        const type = row.original.device_type || "android"

        if (type === "aws_aeum") {
          return (
            <span className="inline-flex items-center gap-1.5 text-muted-foreground">
              <Cloud className="h-4 w-4" />
              <span>{t("devices.awsAeum")}</span>
              <span className="ml-1 text-xs text-muted-foreground">
                {t("devices.awsAeumSubtext")}
              </span>
            </span>
          )
        }

        const isModem = type === "modem"
        const isOnline = isModem && modemStatus?.[row.original.id]

        return (
          <span className="inline-flex items-center gap-1.5 text-muted-foreground">
            {isModem ? (
              <Usb className="h-4 w-4" />
            ) : (
              <Smartphone className="h-4 w-4" />
            )}
            {isModem ? t("devices.usbModem") : t("devices.androidPhone")}
            {isModem && (
              <span
                className={`ml-1 inline-block h-2 w-2 rounded-full ${isOnline ? "bg-emerald-500" : "bg-neutral-300"}`}
                title={isOnline ? t("devices.online") : t("devices.offline")}
              />
            )}
          </span>
        )
      },
    },
    {
      accessorKey: "phone_number",
      header: t("devices.phoneNumber"),
      cell: ({ row }) => (
        <span className="text-muted-foreground">
          {row.original.phone_number}
        </span>
      ),
    },
    {
      accessorKey: "created",
      header: t("common.created"),
      cell: ({ row }) => (
        <span className="text-muted-foreground">
          {formatDate(row.original.created)}
        </span>
      ),
    },
    {
      id: "actions",
      header: () => <span className="sr-only">{t("common.actions")}</span>,
      cell: ({ row }) => (
        <div className="flex justify-end">
          <DeviceActionsMenu device={row.original} />
        </div>
      ),
    },
  ]
}

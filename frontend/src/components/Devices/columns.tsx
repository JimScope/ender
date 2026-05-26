import type { ColumnDef } from "@tanstack/react-table"
import type { TFunction } from "i18next"
import { Server, Smartphone, Usb } from "lucide-react"

import { DeviceActionsMenu } from "@/components/Devices/DeviceActionsMenu"
import { formatDate } from "@/lib/utils"
import type { Device } from "@/types/collections"

export function getColumns(
  t: TFunction,
  modemStatus?: Record<string, boolean>,
  smppStatus?: Record<string, boolean>,
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
        const isModem = type === "modem"
        const isSmpp = type === "smpp"
        const isAgentBacked = isModem || isSmpp
        const isOnline = isModem
          ? modemStatus?.[row.original.id]
          : isSmpp
            ? smppStatus?.[row.original.id]
            : false

        const Icon = isModem ? Usb : isSmpp ? Server : Smartphone
        const label = isModem
          ? t("devices.usbModem")
          : isSmpp
            ? t("devices.smppGateway")
            : t("devices.androidPhone")

        return (
          <span className="inline-flex items-center gap-1.5 text-muted-foreground">
            <Icon className="h-4 w-4" />
            {label}
            {isAgentBacked && (
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

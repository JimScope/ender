import { createFileRoute } from "@tanstack/react-router"
import { Download, Plus, Smartphone } from "lucide-react"
import { Suspense, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import { DataTable } from "@/components/Common/DataTable"
import AddDevice from "@/components/Devices/AddDevice"
import AndroidAppDownload from "@/components/Devices/AndroidAppDownload"
import { getColumns } from "@/components/Devices/columns"
import ModemAgentDownload from "@/components/Devices/ModemAgentDownload"
import PendingDevices from "@/components/Pending/PendingDevices"
import { Button } from "@/components/ui/button"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { Separator } from "@/components/ui/separator"
import useAppConfig from "@/hooks/useAppConfig"
import { useDeviceListSuspense } from "@/hooks/useDeviceList"
import { useModemStatus } from "@/hooks/useModemStatus"
import { useSmppStatus } from "@/hooks/useSmppStatus"
import type { Device } from "@/types/collections"

export const Route = createFileRoute("/_layout/devices")({
  component: Devices,
})

function DevicesEmptyState({ onAddDevice }: { onAddDevice: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-center justify-center text-center py-12">
      <div className="rounded-full bg-muted p-4 mb-4">
        <Smartphone className="h-8 w-8 text-muted-foreground" />
      </div>
      <h2 className="text-lg font-semibold">{t("devices.noDevices")}</h2>
      <p className="text-muted-foreground">{t("devices.noDevicesDesc")}</p>
      <Button className="my-4" onClick={onAddDevice}>
        <Plus />
        {t("devices.addDevice")}
      </Button>
    </div>
  )
}

function DevicesTableContent({ onAddDevice }: { onAddDevice: () => void }) {
  const { t } = useTranslation()
  const { data: devices } = useDeviceListSuspense()
  const { data: modemStatus } = useModemStatus()
  const { data: smppStatus } = useSmppStatus()
  const columns = useMemo(
    () => getColumns(t, modemStatus, smppStatus),
    [t, modemStatus, smppStatus],
  )

  if (!devices?.data || devices.data.length === 0) {
    return <DevicesEmptyState onAddDevice={onAddDevice} />
  }

  return (
    <DataTable
      columns={columns}
      data={(devices?.data ?? []) as unknown as Device[]}
      caption={t("devices.registeredDevices")}
    />
  )
}

function DevicesTable({ onAddDevice }: { onAddDevice: () => void }) {
  return (
    <Suspense fallback={<PendingDevices />}>
      <DevicesTableContent onAddDevice={onAddDevice} />
    </Suspense>
  )
}

function Devices() {
  const { t } = useTranslation()
  const { config } = useAppConfig()
  const [addDeviceOpen, setAddDeviceOpen] = useState(false)

  return (
    <div className="flex flex-col gap-6">
      <title>{`${t("sidebar.devices")} - ${config.appName}`}</title>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl">{t("sidebar.devices")}</h1>
          <p className="text-muted-foreground">{t("devices.description")}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Popover>
            <PopoverTrigger asChild>
              <Button variant="outline">
                <Download className="size-4" />
                {t("devices.downloads")}
              </Button>
            </PopoverTrigger>
            <PopoverContent align="end" className="w-80 space-y-4">
              <AndroidAppDownload />
              <Separator />
              <ModemAgentDownload />
            </PopoverContent>
          </Popover>
          <AddDevice open={addDeviceOpen} onOpenChange={setAddDeviceOpen} />
        </div>
      </div>
      <DevicesTable onAddDevice={() => setAddDeviceOpen(true)} />
    </div>
  )
}

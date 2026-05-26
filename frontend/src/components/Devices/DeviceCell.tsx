import type { LucideIcon } from "lucide-react"
import { Cloud, Smartphone, Usb } from "lucide-react"

import type { Device, DeviceType } from "@/types/collections"

// deviceIcon picks the lucide icon component for a given device_type. Shared
// by the device list (which renders the type label) and any compact cell that
// only shows icon + name.
export function deviceIcon(type?: DeviceType): LucideIcon {
  switch (type) {
    case "aws_aeum":
      return Cloud
    case "modem":
      return Usb
    default:
      return Smartphone
  }
}

interface DeviceCellProps {
  device?: Device
}

// DeviceCell renders icon + device name inline. Used by SMS and ScheduledSMS
// tables to surface which gateway a message belongs to.
export function DeviceCell({ device }: DeviceCellProps) {
  if (!device) {
    return <span className="text-muted-foreground">—</span>
  }
  const Icon = deviceIcon(device.device_type)
  return (
    <span className="inline-flex items-center gap-1.5">
      <Icon className="h-4 w-4 text-muted-foreground" />
      <span className="font-medium">{device.name}</span>
    </span>
  )
}

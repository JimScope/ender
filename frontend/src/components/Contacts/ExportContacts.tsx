import { Download, FileSpreadsheet, FileText } from "lucide-react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import type { Contact, ContactGroup } from "@/types/collections"

interface ExportContactsProps {
  contacts: Contact[]
  groups: ContactGroup[]
}

function downloadFile(content: string, filename: string, mimeType: string) {
  const blob = new Blob([content], { type: mimeType })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}

function resolveGroupNames(groupIds: string[], groups: ContactGroup[]): string {
  return groupIds
    .map((id) => groups.find((g) => g.id === id)?.name ?? "")
    .filter(Boolean)
    .join("; ")
}

function exportCSV(contacts: Contact[], groups: ContactGroup[]) {
  const escapeCSVValue = (value: string) => {
    if (value.includes(",") || value.includes('"') || value.includes("\n")) {
      return `"${value.replace(/"/g, '""')}"`
    }
    return value
  }

  const headers = ["Name", "Phone Number", "Groups", "Notes"]
  const rows = contacts.map((c) => [
    escapeCSVValue(c.name),
    escapeCSVValue(c.phone_number),
    escapeCSVValue(resolveGroupNames(c.groups || [], groups)),
    escapeCSVValue(c.notes || ""),
  ])

  const csv = [headers.join(","), ...rows.map((r) => r.join(","))].join("\n")
  downloadFile(csv, "contacts.csv", "text/csv;charset=utf-8")
}

function exportVCard(contacts: Contact[]) {
  const cards = contacts.map((c) => {
    const lines = [
      "BEGIN:VCARD",
      "VERSION:3.0",
      `FN:${c.name}`,
      `TEL:${c.phone_number}`,
    ]
    if (c.notes) {
      lines.push(`NOTE:${c.notes}`)
    }
    lines.push("END:VCARD")
    return lines.join("\r\n")
  })

  const vcf = cards.join("\r\n")
  downloadFile(vcf, "contacts.vcf", "text/vcard;charset=utf-8")
}

const ExportContacts = ({ contacts, groups }: ExportContactsProps) => {
  const { t } = useTranslation()

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline">
          <Download className="size-4" />
          {t("contacts.export")}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => exportCSV(contacts, groups)}>
          <FileSpreadsheet />
          {t("contacts.exportCSV")}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => exportVCard(contacts)}>
          <FileText />
          {t("contacts.exportVCard")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export default ExportContacts

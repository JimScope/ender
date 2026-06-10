import { Download, FileSpreadsheet, FileText } from "lucide-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import useCustomToast from "@/hooks/useCustomToast"
import pb from "@/lib/pocketbase"
import type { Contact, ContactGroup } from "@/types/collections"

interface ExportContactsProps {
  groups: ContactGroup[]
}

// The list view only downloads the first page of contacts; exports must
// cover every record, so they pull the full list on demand.
async function fetchAllContacts(): Promise<Contact[]> {
  const items = await pb.collection("contacts").getFullList({
    sort: "-created",
  })
  return items as unknown as Contact[]
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

const ExportContacts = ({ groups }: ExportContactsProps) => {
  const { t } = useTranslation()
  const { showErrorToast } = useCustomToast()
  const [isExporting, setIsExporting] = useState(false)

  const handleExport = async (format: "csv" | "vcard") => {
    setIsExporting(true)
    try {
      const contacts = await fetchAllContacts()
      if (format === "csv") {
        exportCSV(contacts, groups)
      } else {
        exportVCard(contacts)
      }
    } catch {
      showErrorToast(t("contacts.exportFailed"))
    } finally {
      setIsExporting(false)
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" disabled={isExporting}>
          <Download className="size-4" />
          {t("contacts.export")}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => handleExport("csv")}>
          <FileSpreadsheet />
          {t("contacts.exportCSV")}
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => handleExport("vcard")}>
          <FileText />
          {t("contacts.exportVCard")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export default ExportContacts

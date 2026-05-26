import { Upload } from "lucide-react"
import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import { MultiSelect } from "@/components/Common/MultiSelect"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import { LoadingButton } from "@/components/ui/loading-button"
import { useContactGroupList } from "@/hooks/useContactGroupList"
import { useContactImport } from "@/hooks/useContactImport"
import useCustomToast from "@/hooks/useCustomToast"

const ImportContacts = () => {
  const { t } = useTranslation()
  const [isOpen, setIsOpen] = useState(false)
  const [file, setFile] = useState<File | null>(null)
  const [selectedGroups, setSelectedGroups] = useState<string[]>([])
  const fileInputRef = useRef<HTMLInputElement>(null)
  const { data: groups } = useContactGroupList()
  const importMutation = useContactImport()
  const { showSuccessToast } = useCustomToast()

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const selected = e.target.files?.[0]
    if (selected) {
      setFile(selected)
    }
  }

  const handleImport = () => {
    if (!file) return

    importMutation.mutate(
      {
        file,
        groupId: selectedGroups[0],
      },
      {
        onSuccess: (result) => {
          showSuccessToast(
            t("contacts.imported", {
              imported: result.imported,
              skipped: result.skipped,
            }),
          )
          handleClose(false)
        },
      },
    )
  }

  const handleClose = (open: boolean) => {
    if (!open) {
      setFile(null)
      setSelectedGroups([])
      if (fileInputRef.current) {
        fileInputRef.current.value = ""
      }
    }
    setIsOpen(open)
  }

  return (
    <Dialog open={isOpen} onOpenChange={handleClose}>
      <DialogTrigger asChild>
        <Button variant="outline">
          <Upload className="size-4" />
          {t("contacts.import")}
        </Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{t("contacts.importVcard")}</DialogTitle>
          <DialogDescription>{t("contacts.importDesc")}</DialogDescription>
        </DialogHeader>

        <div className="grid gap-4 py-4">
          <div className="grid gap-2">
            <label className="text-sm font-medium" htmlFor="vcf-file">
              {t("contacts.selectFile")}
            </label>
            <input
              ref={fileInputRef}
              id="vcf-file"
              type="file"
              accept=".vcf"
              onChange={handleFileChange}
              className="text-sm file:mr-4 file:rounded-md file:border-0 file:bg-primary file:px-4 file:py-2 file:text-sm file:font-medium file:text-primary-foreground hover:file:bg-primary/90 file:cursor-pointer"
            />
          </div>

          <div className="grid gap-2">
            <span className="text-sm font-medium">
              {t("contacts.importToGroup")}
            </span>
            <MultiSelect
              options={(groups?.data || []).map((group) => ({
                label: group.name,
                value: group.id,
              }))}
              onValueChange={setSelectedGroups}
              defaultValue={selectedGroups}
              placeholder={t("contacts.groups")}
              maxVisibleBadges={1}
            />
          </div>
        </div>

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={importMutation.isPending}>
              {t("common.cancel")}
            </Button>
          </DialogClose>
          <LoadingButton
            onClick={handleImport}
            loading={importMutation.isPending}
            disabled={!file}
          >
            {t("contacts.import")}
          </LoadingButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export default ImportContacts

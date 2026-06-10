import { createFileRoute } from "@tanstack/react-router"
import { Plus, Search, Users } from "lucide-react"
import { Suspense, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import { DataTable } from "@/components/Common/DataTable"
import AddContact from "@/components/Contacts/AddContact"
import AddGroup from "@/components/Contacts/AddGroup"
import { getColumns } from "@/components/Contacts/columns"
import ExportContacts from "@/components/Contacts/ExportContacts"
import ImportContacts from "@/components/Contacts/ImportContacts"
import ManageGroups from "@/components/Contacts/ManageGroups"
import PendingContacts from "@/components/Pending/PendingContacts"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import useAppConfig from "@/hooks/useAppConfig"
import { useContactGroupList } from "@/hooks/useContactGroupList"
import { useContactListSuspense } from "@/hooks/useContactList"
import type { Contact, ContactGroup } from "@/types/collections"

export const Route = createFileRoute("/_layout/contacts")({
  component: Contacts,
})

function ContactsEmptyState({ onAddContact }: { onAddContact: () => void }) {
  const { t } = useTranslation()
  return (
    <div className="flex flex-col items-center justify-center text-center py-12">
      <div className="rounded-full bg-muted p-4 mb-4">
        <Users className="h-8 w-8 text-muted-foreground" />
      </div>
      <h2 className="text-lg font-semibold">{t("contacts.noContacts")}</h2>
      <p className="text-muted-foreground">{t("contacts.noContactsDesc")}</p>
      <Button className="my-4" onClick={onAddContact}>
        <Plus />
        {t("contacts.addContact")}
      </Button>
    </div>
  )
}

interface ContactsTableContentProps {
  onAddContact: () => void
  search: string
  groupFilter: string
}

function ContactsTableContent({
  onAddContact,
  search,
  groupFilter,
}: ContactsTableContentProps) {
  const { t } = useTranslation()
  const { data: contacts } = useContactListSuspense()
  const { data: groups } = useContactGroupList()
  const groupList = (groups?.data ?? []) as unknown as ContactGroup[]
  const allContacts = (contacts?.data ?? []) as unknown as Contact[]
  const columns = useMemo(() => getColumns(t, groupList), [t, groupList])

  if (allContacts.length === 0) {
    return <ContactsEmptyState onAddContact={onAddContact} />
  }

  const filteredContacts = allContacts.filter((contact) => {
    const searchLower = search.toLowerCase()
    const matchesSearch =
      !search ||
      contact.name.toLowerCase().includes(searchLower) ||
      contact.phone_number.toLowerCase().includes(searchLower)

    const matchesGroup =
      !groupFilter || (contact.groups || []).includes(groupFilter)

    return matchesSearch && matchesGroup
  })

  return (
    <DataTable
      columns={columns}
      data={filteredContacts}
      caption={t("contacts.title")}
      totalCount={contacts?.count}
      loadedCount={allContacts.length}
    />
  )
}

interface ContactsTableProps {
  onAddContact: () => void
  search: string
  groupFilter: string
}

function ContactsTable({
  onAddContact,
  search,
  groupFilter,
}: ContactsTableProps) {
  return (
    <Suspense fallback={<PendingContacts />}>
      <ContactsTableContent
        onAddContact={onAddContact}
        search={search}
        groupFilter={groupFilter}
      />
    </Suspense>
  )
}

function Contacts() {
  const { t } = useTranslation()
  const { config } = useAppConfig()
  const [addContactOpen, setAddContactOpen] = useState(false)
  const [search, setSearch] = useState("")
  const [groupFilter, setGroupFilter] = useState("")
  const { data: groups } = useContactGroupList()

  const groupList = (groups?.data ?? []) as unknown as ContactGroup[]

  return (
    <div className="flex flex-col gap-6">
      <title>{`${t("contacts.title")} - ${config.appName}`}</title>
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl">{t("contacts.title")}</h1>
          <p className="text-muted-foreground">{t("contacts.description")}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <ExportContacts
            groups={groupList}
            search={search}
            groupFilter={groupFilter === "all" ? "" : groupFilter}
          />
          <ImportContacts />
          <ManageGroups />
          <AddGroup />
          <AddContact open={addContactOpen} onOpenChange={setAddContactOpen} />
        </div>
      </div>
      <div className="flex items-center gap-3">
        <div className="relative flex-1 max-w-sm">
          <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
          <Input
            placeholder={t("contacts.searchPlaceholder")}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9"
          />
        </div>
        <Select value={groupFilter} onValueChange={setGroupFilter}>
          <SelectTrigger className="w-[200px]">
            <SelectValue placeholder={t("contacts.filterByGroup")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t("contacts.allGroups")}</SelectItem>
            {groupList.map((group) => (
              <SelectItem key={group.id} value={group.id}>
                {group.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
      <ContactsTable
        onAddContact={() => setAddContactOpen(true)}
        search={search}
        groupFilter={groupFilter === "all" ? "" : groupFilter}
      />
    </div>
  )
}

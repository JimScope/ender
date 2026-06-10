import { createFileRoute, redirect } from "@tanstack/react-router"
import { Settings, Users } from "lucide-react"
import { Suspense, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import AddUser from "@/components/Admin/AddUser"
import { getColumns, type UserTableData } from "@/components/Admin/columns"
import SystemSettings from "@/components/Admin/SystemSettings"
import { DataTable } from "@/components/Common/DataTable"
import PendingUsers from "@/components/Pending/PendingUsers"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import useAuth from "@/hooks/useAuth"
import { useUserListSuspense } from "@/hooks/useUserList"
import pb from "@/lib/pocketbase"

export const Route = createFileRoute("/_layout/admin")({
  component: Admin,
  // Data is already protected server-side; this guard keeps non-superusers
  // from landing on an error-filled page via direct URL.
  beforeLoad: () => {
    if (!pb.authStore.record?.is_superuser) {
      throw redirect({ to: "/" })
    }
  },
  head: () => ({
    meta: [
      {
        title: "Admin - Vendel",
      },
    ],
  }),
})

function UsersTableContent() {
  const { t } = useTranslation()
  const { user: currentUser } = useAuth()
  const { data: users } = useUserListSuspense()

  const columns = useMemo(() => getColumns(t), [t])

  const tableData = (users?.data ?? []).map((user) => ({
    ...user,
    isCurrentUser: currentUser?.id === user.id,
  })) as unknown as UserTableData[]

  return (
    <DataTable
      columns={columns}
      data={tableData}
      caption={t("admin.users")}
      totalCount={users?.count}
    />
  )
}

function UsersTable() {
  return (
    <Suspense fallback={<PendingUsers />}>
      <UsersTableContent />
    </Suspense>
  )
}

function Admin() {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState("users")

  return (
    <div className="flex flex-col gap-6">
      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <div className="flex items-center justify-between">
          <TabsList>
            <TabsTrigger value="users" className="gap-2">
              <Users className="h-4 w-4" />
              {t("admin.users")}
            </TabsTrigger>
            <TabsTrigger value="settings" className="gap-2">
              <Settings className="h-4 w-4" />
              {t("admin.settings")}
            </TabsTrigger>
          </TabsList>

          {activeTab === "users" && <AddUser />}
        </div>

        <TabsContent value="users" className="mt-6">
          <div className="mb-4">
            <h1 className="text-2xl">{t("admin.users")}</h1>
            <p className="text-muted-foreground">{t("admin.userManagement")}</p>
          </div>
          <UsersTable />
        </TabsContent>

        <TabsContent value="settings" className="mt-6">
          <div className="mb-4">
            <h1 className="text-2xl">{t("admin.systemSettings")}</h1>
            <p className="text-muted-foreground">
              {t("admin.systemSettingsDesc")}
            </p>
          </div>
          <SystemSettings />
        </TabsContent>
      </Tabs>
    </div>
  )
}

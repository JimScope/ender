import { createFileRoute, useNavigate } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import { z } from "zod"

import ChangePassword from "@/components/UserSettings/ChangePassword"
import DeleteAccount from "@/components/UserSettings/DeleteAccount"
import PlanSettings from "@/components/UserSettings/PlanSettings"
import UserInformation from "@/components/UserSettings/UserInformation"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import useAuth from "@/hooks/useAuth"

const tabsConfig = [
  {
    value: "my-profile",
    titleKey: "settings.myProfile" as const,
    component: UserInformation,
  },
  {
    value: "password",
    titleKey: "settings.password" as const,
    component: ChangePassword,
  },
  {
    value: "plan",
    titleKey: "settings.planUsage" as const,
    component: PlanSettings,
  },
  {
    value: "danger-zone",
    titleKey: "settings.dangerZone" as const,
    component: DeleteAccount,
  },
]

const searchSchema = z.object({
  tab: z.string().optional().catch(undefined),
})

export const Route = createFileRoute("/_layout/settings")({
  component: UserSettings,
  validateSearch: searchSchema,
  head: () => ({
    meta: [
      {
        title: "Settings - Vendel",
      },
    ],
  }),
})

function UserSettings() {
  const { t } = useTranslation()
  const { user: currentUser } = useAuth()
  const { tab } = Route.useSearch()
  const navigate = useNavigate()
  // Superusers must not see the danger zone (deleting the only admin would
  // brick the instance) — drop the last tab for them.
  const finalTabs = currentUser?.is_superuser
    ? tabsConfig.slice(0, 3)
    : tabsConfig

  const activeTab = finalTabs.some((tb) => tb.value === tab)
    ? tab
    : "my-profile"

  if (!currentUser) {
    return null
  }

  return (
    <div className="flex flex-col gap-6">
      <div>
        <h1 className="text-2xl">{t("settings.title")}</h1>
        <p className="text-muted-foreground">{t("settings.description")}</p>
      </div>

      <Tabs
        value={activeTab}
        onValueChange={(value) =>
          navigate({
            to: "/settings",
            search: { tab: value },
            replace: true,
          })
        }
      >
        <TabsList>
          {finalTabs.map((tb) => (
            <TabsTrigger key={tb.value} value={tb.value}>
              {t(tb.titleKey)}
            </TabsTrigger>
          ))}
        </TabsList>
        {finalTabs.map((tab) => (
          <TabsContent key={tab.value} value={tab.value}>
            <tab.component />
          </TabsContent>
        ))}
      </Tabs>
    </div>
  )
}

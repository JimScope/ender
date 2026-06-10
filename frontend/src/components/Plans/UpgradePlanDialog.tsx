import { useQueryClient } from "@tanstack/react-query"
import { Check } from "lucide-react"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
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
import { Skeleton } from "@/components/ui/skeleton"
import { useBalance } from "@/hooks/useBalance"
import useCustomToast from "@/hooks/useCustomToast"
import { usePlanList } from "@/hooks/usePlanList"
import { useUpgradePlan } from "@/hooks/usePlanMutations"
import type { Plan } from "@/types/collections"

interface UpgradePlanDialogProps {
  currentPlan?: string
}

function PlanCard({
  plan,
  isCurrentPlan,
  isSelected,
  onSelect,
}: {
  plan: Plan
  isCurrentPlan: boolean
  isSelected: boolean
  onSelect: () => void
}) {
  const { t } = useTranslation()
  return (
    <Card
      className={`relative cursor-pointer transition-all ${isCurrentPlan ? "bg-accent/30" : ""}`}
      style={
        isSelected
          ? {
              borderColor: "var(--brand)",
              boxShadow:
                "0 0 0 3px color-mix(in srgb, var(--brand) 25%, transparent)",
            }
          : undefined
      }
      onClick={onSelect}
    >
      {isCurrentPlan && (
        <div className="absolute -top-3 left-4 bg-primary text-primary-foreground text-xs px-2 py-1 rounded">
          {t("plans.currentPlan")}
        </div>
      )}
      <CardHeader className="pb-2">
        <CardTitle className="capitalize">{plan.name}</CardTitle>
        <CardDescription>
          <span className="text-2xl font-bold text-foreground">
            ${plan.price?.toFixed(2) ?? "0.00"}
          </span>
          <span className="text-muted-foreground">{t("plans.perMonth")}</span>
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ul className="space-y-2 text-sm">
          <li className="flex items-center gap-2">
            <Check className="h-4 w-4 text-primary" />
            {t("plans.smsPerMonth", { count: plan.max_sms_per_month ?? 0 })}
          </li>
          <li className="flex items-center gap-2">
            <Check className="h-4 w-4 text-primary" />
            {t("plans.device", { count: plan.max_devices ?? 0 })}
          </li>
        </ul>
      </CardContent>
    </Card>
  )
}

function UpgradePlanDialog({ currentPlan }: UpgradePlanDialogProps) {
  const { t } = useTranslation()
  const [isOpen, setIsOpen] = useState(false)
  const [selectedPlanId, setSelectedPlanId] = useState<string | null>(null)
  const { data: plansData, isLoading } = usePlanList()
  const { data: balance } = useBalance()
  const queryClient = useQueryClient()
  const { showSuccessToast, showErrorToast } = useCustomToast()
  const mutation = useUpgradePlan()

  const selectedPlan = plansData?.data?.find((p) => p.id === selectedPlanId)

  const handleSubmit = () => {
    if (!selectedPlanId) return

    mutation.mutate(
      { plan_id: selectedPlanId, billing_cycle: "monthly" },
      {
        onSuccess: (data) => {
          queryClient.invalidateQueries({ queryKey: ["quota"] })
          queryClient.invalidateQueries({ queryKey: ["balance"] })
          if (data.status.includes("active")) {
            showSuccessToast(t("plans.planActivated"))
          } else {
            // The backend returns "pending" when the subscription awaits
            // payment confirmation — give feedback instead of staying silent.
            showSuccessToast(t("plans.planPending"))
          }
          setIsOpen(false)
          setSelectedPlanId(null)
        },
        onError: (error) => {
          showErrorToast(error instanceof Error ? error.message : String(error))
        },
      },
    )
  }

  const handleOpenChange = (open: boolean) => {
    setIsOpen(open)
    if (!open) {
      setSelectedPlanId(null)
    }
  }

  const isCurrentPlanSelected =
    selectedPlan?.name.toLowerCase() === currentPlan?.toLowerCase()

  const canSubmit = selectedPlanId && !isCurrentPlanSelected

  return (
    <Dialog open={isOpen} onOpenChange={handleOpenChange}>
      <DialogTrigger asChild>
        <Button size="sm">{t("plans.upgradePlan")}</Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("plans.changePlan")}</DialogTitle>
          <DialogDescription>{t("plans.choosePlanDesc")}</DialogDescription>
        </DialogHeader>

        {isLoading ? (
          <div className="grid gap-4 grid-cols-1 sm:grid-cols-3">
            {["a", "b", "c"].map((id) => (
              <Skeleton key={id} className="h-48 w-full" />
            ))}
          </div>
        ) : (
          <div className="space-y-6">
            <p className="text-sm text-muted-foreground">
              {t("plans.currentBalance", {
                amount: `$${balance?.balance?.toFixed(2) ?? "0.00"}`,
              })}
            </p>

            <div className="grid gap-4 grid-cols-1 sm:grid-cols-3">
              {(plansData?.data as unknown as Plan[])?.map((plan) => (
                <PlanCard
                  key={plan.id}
                  plan={plan}
                  isCurrentPlan={
                    plan.name.toLowerCase() === currentPlan?.toLowerCase()
                  }
                  isSelected={selectedPlanId === plan.id}
                  onSelect={() => setSelectedPlanId(plan.id)}
                />
              ))}
            </div>
          </div>
        )}

        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline" disabled={mutation.isPending}>
              {t("common.cancel")}
            </Button>
          </DialogClose>
          <LoadingButton
            onClick={handleSubmit}
            loading={mutation.isPending}
            disabled={!canSubmit}
          >
            {isCurrentPlanSelected
              ? t("plans.currentPlan")
              : t("plans.confirmUpgrade")}
          </LoadingButton>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export default UpgradePlanDialog

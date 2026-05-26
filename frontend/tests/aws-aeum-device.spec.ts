import { expect, test } from "@playwright/test"

test.describe("AWS End User Messaging device", () => {
  test("appears in the device list when present in API response", async ({
    page,
  }) => {
    // Stub the sms_devices list endpoint so the AEUM virtual device is
    // returned regardless of backend state. The test does not require
    // AEUM_ENABLED on the backend; it only validates the frontend
    // renders the row when the API exposes it.
    await page.route(
      "**/api/collections/sms_devices/records*",
      async (route) => {
        await route.fulfill({
          contentType: "application/json",
          body: JSON.stringify({
            page: 1,
            perPage: 100,
            totalItems: 1,
            totalPages: 1,
            items: [
              {
                id: "aeum1",
                collectionId: "sms_devices",
                collectionName: "sms_devices",
                created: new Date().toISOString(),
                updated: new Date().toISOString(),
                name: "AWS End User Messaging",
                phone_number: "aws-aeum",
                device_type: "aws_aeum",
                user: "",
              },
            ],
          }),
        })
      },
    )

    await page.goto("/devices")
    await expect(page.getByText("AWS End User Messaging")).toBeVisible()
  })
})

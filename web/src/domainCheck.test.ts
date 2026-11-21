import { describe, expect, it } from "vitest";
import type { Provider } from "./api";
import { domainOf, serviceOf } from "./pages/DomainCheck";

describe("domain check", () => {
  it("finds the sending domain and service", () => {
    const p = { id: 1, channel: "email", config: { from: "Acme <news@Mail.Acme.test>", host: "email-smtp.ap-south-1.amazonaws.com" } } as unknown as Provider;
    expect(domainOf([p])).toBe("mail.acme.test");
    expect(serviceOf(p)).toBe("ses");
    expect(serviceOf({ ...p, config: { service: "sendgrid" } } as unknown as Provider)).toBe("sendgrid");
    expect(domainOf([])).toBe("");
  });
});

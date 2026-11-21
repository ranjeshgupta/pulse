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

  it("matches a service by the host's domain, not by a substring of it", () => {
    const at = (host: string) => serviceOf({ id: 1, channel: "email", config: { host } } as unknown as Provider);
    expect(at("smtp.sendgrid.net")).toBe("sendgrid");
    expect(at("SMTP.Mailgun.org:587")).toBe("mailgun");
    expect(at("smtp://smtp-relay.gmail.com:465/")).toBe("gmail");
    expect(at("smtp.postmarkapp.com.")).toBe("postmark");
    expect(at("amazonaws.com")).toBe("ses");
    // A look-alike host is not the service, so its SPF include is neither checked nor advised.
    expect(at("email-smtp.amazonaws.com.relay.example.net")).toBe("");
    expect(at("notamazonaws.com")).toBe("");
    expect(at("mail.google-partner.example")).toBe("");
    expect(at("mysendgrid-relay.example.com")).toBe("");
    expect(at("")).toBe("");
  });
});

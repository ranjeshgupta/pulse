import { describe, expect, it } from "vitest";
import { providerOptions } from "./ProviderPick";

describe("provider choice", () => {
  it("puts the default first as 0, then the others by name", () => {
    expect(providerOptions([{ id: 3, name: "Twilio", is_default: false }, { id: 1, name: "Gupshup", is_default: true }, { id: 2, name: "MSG91", is_default: false }]))
      .toEqual([{ value: 0, label: "Gupshup (default)" }, { value: 2, label: "MSG91" }, { value: 3, label: "Twilio" }]);
    expect(providerOptions([])[0]).toEqual({ value: 0, label: "The channel's default" });
  });
});

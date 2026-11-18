import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { App } from "./App.tsx";
import type { Role } from "./auth/session.tsx";
import { fail, me, mockApi, ok, sentTo } from "./test/api.ts";

afterEach(() => vi.restoreAllMocks());

const dueSummary = {
  id: "d1",
  name: "Cuota de noviembre",
  amountCents: 200000,
  dueDate: "2026-11-30",
  createdAt: "2026-11-16T10:00:00Z",
  owingCount: 3,
  totalCount: 3,
};

const owingDetail = {
  ...dueSummary,
  members: [
    { rosterMemberId: "r1", fullName: "Ana Ficticia", status: "owing" },
    { rosterMemberId: "r2", fullName: "Beto Ficticio", status: "owing" },
    { rosterMemberId: "r3", fullName: "Carla Ficticia", status: "owing" },
  ],
};

// S4 without payments: 3 members, Beto exempt -> Ana and Carla owe, "0 de 2 pagaron".
const exemptDetail = {
  ...dueSummary,
  owingCount: 2,
  totalCount: 2,
  members: [
    { rosterMemberId: "r1", fullName: "Ana Ficticia", status: "owing" },
    { rosterMemberId: "r2", fullName: "Beto Ficticio", status: "exempt", exemptionReason: "Beca" },
    { rosterMemberId: "r3", fullName: "Carla Ficticia", status: "owing" },
  ],
};

const officer = () => me({ role: "officer", roles: ["student", "officer"], mfa: "satisfied" });

async function openDetail() {
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  fireEvent.click(await screen.findByRole("button", { name: "Ver detalle de Cuota de noviembre" }));
  await screen.findByText("Beto Ficticio");
}

/** Text of every element the input is described by (hint and error). */
function describedText(input: HTMLElement): string {
  return (input.getAttribute("aria-describedby") ?? "")
    .split(" ")
    .map((id) => document.getElementById(id)?.textContent ?? "")
    .join(" ");
}

function row(name: string): HTMLElement {
  return screen.getByRole("rowheader", { name }).closest("tr") as HTMLElement;
}

test("S1 S6 officer exempts a member with a reason: POST sends rosterMemberId and reason, member shows Exento with the reason", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(officer()),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([]),
    "GET /finance/dues/d1": ok(owingDetail),
    "POST /finance/dues/d1/exemptions": ok(exemptDetail, 201),
  });
  await openDetail();

  fireEvent.click(within(row("Beto Ficticio")).getByRole("button", { name: "Eximir a Beto Ficticio" }));
  const reason = screen.getByLabelText("Motivo") as HTMLInputElement;
  expect(document.activeElement).toBe(reason);
  fireEvent.change(reason, { target: { value: "  Beca  " } });
  fireEvent.click(screen.getByRole("button", { name: "Confirmar exención" }));

  await waitFor(() => expect(sentTo(sent, "POST /finance/dues/d1/exemptions")).toHaveLength(1));
  expect(sentTo(sent, "POST /finance/dues/d1/exemptions")[0]?.body).toEqual({ rosterMemberId: "r2", reason: "Beca" });

  await waitFor(() => expect(within(row("Beto Ficticio")).getByText("Exento")).toBeTruthy());
  expect(within(row("Beto Ficticio")).getByText("Motivo: Beca")).toBeTruthy();
  expect(within(row("Beto Ficticio")).queryByRole("button", { name: /Eximir/ })).toBeNull();
  expect(within(row("Ana Ficticia")).getByRole("button", { name: "Eximir a Ana Ficticia" })).toBeTruthy();
  expect(screen.queryByLabelText("Motivo")).toBeNull();
  expect(screen.getByText("Se registró la exención.")).toBeTruthy();
});

test("S2 empty or too long reason shows the Spanish message, marks Motivo invalid and sends nothing", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(officer()),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([]),
    "GET /finance/dues/d1": ok(owingDetail),
    "POST /finance/dues/d1/exemptions": ok(exemptDetail, 201),
  });
  await openDetail();

  fireEvent.click(screen.getByRole("button", { name: "Eximir a Beto Ficticio" }));
  const reason = screen.getByLabelText("Motivo") as HTMLInputElement;
  fireEvent.change(reason, { target: { value: "   " } });
  fireEvent.click(screen.getByRole("button", { name: "Confirmar exención" }));

  expect(reason.getAttribute("aria-invalid")).toBe("true");
  expect(describedText(reason)).toContain(
    "Escribí el motivo de la exención.",
  );
  expect(document.activeElement).toBe(reason);

  fireEvent.change(reason, { target: { value: "x".repeat(501) } });
  fireEvent.click(screen.getByRole("button", { name: "Confirmar exención" }));
  expect(describedText(reason)).toContain(
    "El motivo puede tener hasta 500 caracteres.",
  );
  expect(sentTo(sent, "POST /finance/dues/d1/exemptions")).toHaveLength(0);
});

test("S3 S8 S9 API errors already_exempt, not_owed and reason_too_long show Spanish messages without the reason or the name", async () => {
  const sent = mockApi({
    "GET /auth/me": ok(officer()),
    "GET /finance/dues": ok([dueSummary]),
    "GET /finance/dues/mine": ok([]),
    "GET /finance/dues/d1": ok(owingDetail),
    "POST /finance/dues/d1/exemptions": [
      fail(409, "already_exempt"),
      fail(422, "not_owed"),
      fail(422, "reason_too_long"),
    ],
  });
  await openDetail();

  fireEvent.click(screen.getByRole("button", { name: "Eximir a Beto Ficticio" }));
  fireEvent.change(screen.getByLabelText("Motivo"), { target: { value: "Beca secreta" } });

  const expected = [
    "Este alumno ya está exento.",
    "Este alumno no debe esta cuota.",
    "El motivo puede tener hasta 500 caracteres.",
  ];
  for (const [i, message] of expected.entries()) {
    fireEvent.click(screen.getByRole("button", { name: "Confirmar exención" }));
    await waitFor(() => expect(sentTo(sent, "POST /finance/dues/d1/exemptions")).toHaveLength(i + 1));
    const status = (await screen.findByText(message)).closest(".form-status") as HTMLElement;
    expect(status.textContent).not.toContain("Beca");
    expect(status.textContent).not.toContain("Beto");
    await waitFor(() => expect(document.activeElement).toBe(status));
  }
  expect(within(row("Beto Ficticio")).getByText("Debe")).toBeTruthy();
});

test("S4 detail shows 0 de 2 pagaron for 3 members with 1 exempt and no payments", async () => {
  mockApi({
    "GET /auth/me": ok(officer()),
    "GET /finance/dues": ok([{ ...dueSummary, owingCount: 2, totalCount: 2 }]),
    "GET /finance/dues/mine": ok([]),
    "GET /finance/dues/d1": ok(exemptDetail),
  });
  await openDetail();
  expect(screen.getByText(/0 de 2 pagaron/)).toBeTruthy();
  expect(within(row("Ana Ficticia")).getByText("Debe")).toBeTruthy();
  expect(within(row("Carla Ficticia")).getByText("Debe")).toBeTruthy();
  expect(within(row("Beto Ficticio")).getByText("Exento")).toBeTruthy();
});

test("S6 S7 school admin and veedor see the exemption reason without Eximir; a student never sees Eximir", async () => {
  for (const role of ["school_admin", "veedor"]) {
    mockApi({
      "GET /auth/me": ok(me({ role: role as Role, mfa: "satisfied" })),
      "GET /finance/dues": ok([dueSummary]),
      "GET /finance/dues/d1": ok(exemptDetail),
    });
    window.history.replaceState(null, "", "/cuotas");
    const { unmount } = render(<App />);
    fireEvent.click(await screen.findByRole("button", { name: "Ver detalle de Cuota de noviembre" }));
    expect(await screen.findByText("Motivo: Beca")).toBeTruthy();
    expect(screen.queryByRole("button", { name: /Eximir/ })).toBeNull();
    unmount();
  }

  mockApi({
    "GET /auth/me": ok(me()),
    "GET /finance/dues/mine": ok([{ id: "d1", name: "Cuota de noviembre", amountCents: 200000, dueDate: "2026-11-30", status: "owing" }]),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  expect(await screen.findByText(/Debés/)).toBeTruthy();
  expect(screen.queryByRole("button", { name: /Eximir/ })).toBeNull();
});

test("S6 Mis cuotas shows only Exento for an exempt due and no reason", async () => {
  mockApi({
    "GET /auth/me": ok(me()),
    "GET /finance/dues/mine": ok([{ id: "d1", name: "Cuota de noviembre", amountCents: 200000, dueDate: "2026-11-30", status: "exempt" }]),
  });
  window.history.replaceState(null, "", "/cuotas");
  render(<App />);
  const item = (await screen.findByText("Cuota de noviembre")).closest("li") as HTMLElement;
  await waitFor(() => expect(within(item).getByText("Exento")).toBeTruthy());
  expect(item.textContent).toBe("Cuota de noviembreExento");
  expect(document.body.textContent).not.toContain("Motivo");
  expect(document.body.textContent).not.toContain("Exenta");
});

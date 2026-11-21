// Product catalog (O6): the products messages can recommend ({{block.recommendations}},
// {{reco.1.name}}): add or edit one, import a CSV, delete with a confirmation, and preview what a
// contact would be shown.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useRef, useState } from "react";
import { api, can } from "../api";
import { useMe } from "../auth";
import { chunks, parseCsv, priceText, toProducts, type Product } from "../catalogCsv";
import { ask, askDelete, type AskOptions } from "../guard";
import { useDebounced } from "../insights";
import { Empty, ErrorNote, Field, Modal, PageHead, timeAgo, useToast } from "../ui";
import "./catalog.css";

const PAGE = 50;

export function ProductCatalog() {
  const [q, setQ] = useState("");
  const [page, setPage] = useState(0);
  const dq = useDebounced(q.trim());
  const list = useQuery({
    queryKey: ["products", dq, page],
    queryFn: () => api.get<{ products: Product[]; total: number }>(`/v1/products?q=${encodeURIComponent(dq)}&limit=${PAGE}&offset=${page * PAGE}`),
    placeholderData: (p) => p,
  });
  const canEdit = can(useMe().data?.permissions, "content", "edit");
  const [editing, setEditing] = useState<Product | null>(null);
  const [importing, setImporting] = useState(false);
  const qc = useQueryClient();
  const toast = useToast();
  const refresh = useMutation({
    mutationFn: () => api.post<{ popular: number; trending: number; bought_together: number }>("/v1/products/refresh-lists", {}),
    onSuccess: (r) => toast({ text: `Lists updated: ${r.popular} popular, ${r.trending} trending, bought-together for ${r.bought_together} products` }),
  });
  const remove = useMutation({
    mutationFn: (code: string) => api.del(`/v1/products/${encodeURIComponent(code)}`),
    onSuccess: (_, code) => { qc.invalidateQueries({ queryKey: ["products"] }); toast({ text: `Deleted ${code}` }); },
  });
  const products = list.data?.products ?? [];
  const total = list.data?.total ?? 0;
  return (
    <div className="page">
      <PageHead title="Product catalog"
        intro={<>The products your messages can recommend. Put <code>{"{{block.recommendations}}"}</code> in an email for a grid of picks, or <code>{"{{reco.1.name}}"}</code> anywhere: each contact gets products bought together with what they bought or looked at, then what's trending and popular — in stock, and not something they just bought.</>}
        actions={canEdit ? <>
          <button className="btn" disabled={refresh.isPending} title="Popular, trending and bought-together lists are worked out every day; this does it now" onClick={() => refresh.mutate()}>{refresh.isPending ? "Updating…" : "Update lists"}</button>
          <button className="btn" onClick={() => setImporting(true)}>Import CSV</button>
          <button className="btn primary" onClick={() => setEditing({ code: "", name: "", in_stock: true })}>Add product</button>
        </> : undefined} />
      <ErrorNote error={list.error ?? refresh.error ?? remove.error} />
      <div className="catalog-bar">
        <input className="input" type="search" placeholder="Search by code, name or category" value={q} aria-label="Search products"
          onChange={(e) => { setQ(e.target.value); setPage(0); }} data-no-dirty />
        <span className="small muted">{total.toLocaleString("en-IN")} product{total === 1 ? "" : "s"}</span>
      </div>
      {products.length === 0 && !list.isLoading ? (
        <div className="panel"><Empty title={dq ? "No products match" : "No products yet"}>
          {dq ? "Try another word." : <>Import a CSV with <code>code</code> (or <code>sku</code>), <code>name</code>, <code>url</code>, <code>image_url</code>, <code>price</code>, <code>category</code> and <code>in_stock</code>, or send them with <code>PUT /v1/products</code>. Events carrying <code>product_code</code> (or <code>items[].product_code</code>) tell Pulse what each contact viewed and bought.</>}
        </Empty></div>
      ) : (
        <div className="panel"><div className="table-scroll"><table className="table catalog-table">
          <thead><tr><th aria-label="Image" /><th>Product</th><th>Category</th><th className="num">Price</th><th>Stock</th><th>Changed</th>{canEdit && <th />}</tr></thead>
          <tbody>{products.map((p) => (
            <tr key={p.code} className={canEdit ? "clickable" : undefined} onClick={canEdit ? () => setEditing(p) : undefined}>
              <td className="catalog-thumb">{p.image_url ? <img src={p.image_url} alt="" loading="lazy" /> : <span aria-hidden="true">🛍️</span>}</td>
              <td><strong>{p.name}</strong><div className="xs faint"><code>{p.code}</code>{p.url && <> · <a href={p.url} target="_blank" rel="noreferrer" onClick={(e) => e.stopPropagation()}>open</a></>}</div></td>
              <td className="small">{p.category || <span className="faint">–</span>}</td>
              <td className="num small">{priceText(p) || <span className="faint">–</span>}</td>
              <td>{p.in_stock ? <span className="badge ok">In stock</span> : <span className="badge">Out of stock</span>}</td>
              <td className="small muted">{p.updated_at ? timeAgo(p.updated_at) : ""}</td>
              {canEdit && <td className="num"><button className="btn ghost sm" aria-label={`Delete ${p.name}`} disabled={remove.isPending}
                onClick={(e) => { e.stopPropagation(); void askDelete(p.name, "Messages stop recommending it. Its events stay.").then((ok) => ok && remove.mutate(p.code)); }}>Delete</button></td>}
            </tr>
          ))}</tbody>
        </table></div></div>
      )}
      {total > PAGE && (
        <div className="row" style={{ justifyContent: "flex-end", marginTop: 10 }}>
          <button className="btn sm" disabled={page === 0} onClick={() => setPage(page - 1)}>Previous</button>
          <span className="small muted">{page * PAGE + 1}–{Math.min(total, (page + 1) * PAGE)} of {total.toLocaleString("en-IN")}</span>
          <button className="btn sm" disabled={(page + 1) * PAGE >= total} onClick={() => setPage(page + 1)}>Next</button>
        </div>
      )}
      <RecoPreview />
      {editing && <ProductEditor product={editing} onClose={() => setEditing(null)} />}
      {importing && <ImportCsv onClose={() => setImporting(false)} />}
    </div>
  );
}

function ProductEditor({ product, onClose }: { product: Product; onClose: () => void }) {
  const isNew = !product.code;
  const [p, setP] = useState(product);
  const qc = useQueryClient();
  const toast = useToast();
  const save = useMutation({
    mutationFn: () => api.put("/v1/products", { products: [{ ...p, code: p.code.trim() }] }),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["products"] }); toast({ text: `Saved ${p.name}` }); onClose(); },
  });
  const num = (v: string) => (v.trim() === "" ? undefined : Number(v));
  return (
    <Modal wide title={isNew ? "Add a product" : `Edit ${product.name}`} onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={save.isPending || !p.code.trim() || !p.name.trim()} onClick={() => save.mutate()}>{save.isPending ? "Saving…" : "Save product"}</button>
    </>}>
      <ErrorNote error={save.error} />
      <div className="grid-2">
        <Field label="Code (SKU)" hint={isNew ? "The product_code your events send." : "The code can't change; add a new product instead."}>
          <input className="input code" autoFocus={isNew} disabled={!isNew} value={p.code} onChange={(e) => setP({ ...p, code: e.target.value })} />
        </Field>
        <Field label="Name"><input className="input" autoFocus={!isNew} value={p.name} onChange={(e) => setP({ ...p, name: e.target.value })} /></Field>
        <Field label="Link"><input className="input" type="url" placeholder="https://shop.example.com/p/…" value={p.url ?? ""} onChange={(e) => setP({ ...p, url: e.target.value })} /></Field>
        <Field label="Image" hint="An https address."><input className="input" type="url" placeholder="https://cdn.example.com/…jpg" value={p.image_url ?? ""} onChange={(e) => setP({ ...p, image_url: e.target.value })} /></Field>
        <Field label="Price"><input className="input" type="number" min={0} step="any" value={p.price ?? ""} onChange={(e) => setP({ ...p, price: num(e.target.value) })} /></Field>
        <Field label="Sale price (optional)"><input className="input" type="number" min={0} step="any" value={p.sale_price ?? ""} onChange={(e) => setP({ ...p, sale_price: num(e.target.value) })} /></Field>
        <Field label="Currency"><input className="input" maxLength={3} placeholder="INR" value={p.currency ?? ""} onChange={(e) => setP({ ...p, currency: e.target.value.toUpperCase() })} /></Field>
        <Field label="Category"><input className="input" value={p.category ?? ""} onChange={(e) => setP({ ...p, category: e.target.value })} /></Field>
      </div>
      <label className="check"><input type="checkbox" checked={p.in_stock} onChange={(e) => setP({ ...p, in_stock: e.target.checked })} />In stock (only products in stock are recommended)</label>
    </Modal>
  );
}

/**
 * importCloseQuestion: what closing the import dialog must ask first, or null to just close. While
 * the chunked upload runs, closing stops it (the chunks sent so far stay imported); a file read but
 * not imported yet is lost on closing.
 */
export function importCloseQuestion(s: { uploading: boolean; ready: number; done: number }): AskOptions | null {
  if (s.uploading) {
    return { title: "Stop the import?", body: `${s.done.toLocaleString("en-IN")} of ${s.ready.toLocaleString("en-IN")} products are imported so far. Those stay; the rest aren't imported.`,
      confirmText: "Stop importing", cancelText: "Keep importing", danger: true };
  }
  if (s.ready > 0) {
    return { title: "Discard this file?", body: "Its products aren't imported yet. If you close now, nothing is imported.", confirmText: "Discard", cancelText: "Keep it", danger: true };
  }
  return null;
}

function ImportCsv({ onClose }: { onClose: () => void }) {
  const [parsed, setParsed] = useState<ReturnType<typeof toProducts> | null>(null);
  const [file, setFile] = useState("");
  const [done, setDone] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  // Set when the dialog is closed during the upload: no further chunks are sent.
  const stopped = useRef(false);
  const qc = useQueryClient();
  const toast = useToast();
  const upload = useMutation({
    mutationFn: async () => {
      let n = 0;
      for (const part of chunks(parsed!.products)) {
        if (stopped.current) break;
        await api.put("/v1/products", { products: part });
        n += part.length;
        setDone(n);
      }
      return { n, stopped: stopped.current };
    },
    onSuccess: ({ n, stopped: early }) => {
      qc.invalidateQueries({ queryKey: ["products"] });
      toast({ text: early ? `Import stopped: ${n.toLocaleString("en-IN")} products imported` : `Imported ${n.toLocaleString("en-IN")} products` });
      if (!early) onClose();
    },
    onError: () => qc.invalidateQueries({ queryKey: ["products"] }),
  });
  const read = async (f: File | undefined) => {
    if (!f) return;
    setFile(f.name);
    setParsed(toProducts(parseCsv(await f.text())));
  };
  const n = parsed?.products.length ?? 0;
  // Esc, the backdrop, Close and Cancel all come here (the dialog's own typing guard is off: a
  // dropped file isn't typing).
  const tryClose = async () => {
    const q = importCloseQuestion({ uploading: upload.isPending, ready: n, done });
    if (q && !(await ask(q))) return;
    if (upload.isPending) stopped.current = true;
    onClose();
  };
  return (
    <Modal wide title="Import products from a CSV" onClose={() => void tryClose()} guard={false} footer={<>
      <button className="btn" onClick={() => void tryClose()}>{upload.isPending ? "Stop" : "Cancel"}</button>
      <button className="btn primary" disabled={!n || upload.isPending} onClick={() => upload.mutate()}>
        {upload.isPending ? `Importing… ${done.toLocaleString("en-IN")} of ${n.toLocaleString("en-IN")}` : `Import ${n.toLocaleString("en-IN")} product${n === 1 ? "" : "s"}`}
      </button>
    </>}>
      <ErrorNote error={upload.error} />
      <p className="small muted" style={{ marginTop: 0 }}>
        Columns: <code>code</code> (or <code>sku</code>, <code>id</code>), <code>name</code> (or <code>title</code>), <code>url</code>, <code>image_url</code>, <code>price</code>, <code>sale_price</code>, <code>currency</code>, <code>category</code>, <code>in_stock</code> (yes / no, or a quantity).
        Products with a code already in the catalog are replaced. A Google Merchant feed export works as it is.
      </p>
      <div className="catalog-drop" onDragOver={(e) => e.preventDefault()} onDrop={(e) => { e.preventDefault(); void read(e.dataTransfer.files[0]); }}>
        <input ref={input} type="file" accept=".csv,text/csv" hidden onChange={(e) => void read(e.target.files?.[0])} />
        <button type="button" className="btn" onClick={() => input.current?.click()}>Choose a file</button>
        <span className="small muted">{file || "or drop a CSV here"}</span>
      </div>
      {parsed && (
        <div aria-live="polite">
          <p className="small"><strong>{n.toLocaleString("en-IN")}</strong> product{n === 1 ? "" : "s"} ready{parsed.unknown.length ? `; columns not used: ${parsed.unknown.join(", ")}` : ""}.</p>
          {parsed.problems.length > 0 && <div className="alert info"><ul style={{ margin: 0, paddingLeft: 18 }}>{parsed.problems.map((x, i) => <li key={i} className="small">{x}</li>)}</ul></div>}
          {n > 0 && (
            <div className="table-scroll"><table className="table"><thead><tr><th>Code</th><th>Name</th><th className="num">Price</th><th>Stock</th></tr></thead>
              <tbody>{parsed.products.slice(0, 5).map((p) => <tr key={p.code}><td><code>{p.code}</code></td><td>{p.name}</td><td className="num">{priceText(p)}</td><td>{p.in_stock ? "In stock" : "Out"}</td></tr>)}</tbody>
            </table></div>
          )}
        </div>
      )}
    </Modal>
  );
}

function RecoPreview() {
  const [ext, setExt] = useState("");
  const [asked, setAsked] = useState("");
  const q = useQuery({
    queryKey: ["contact-reco", asked],
    queryFn: () => api.get<{ products: Product[]; last_viewed: Product | null; bought: string[]; viewed: string[] }>(`/v1/contacts/${encodeURIComponent(asked)}/recommendations`),
    enabled: asked !== "", retry: false,
  });
  return (
    <section className="panel" style={{ marginTop: 18 }}>
      <div className="panel-head"><h3>What would a contact see?</h3></div>
      <div className="panel-body">
        <form className="row" onSubmit={(e) => { e.preventDefault(); setAsked(ext.trim()); }}>
          <input className="input" style={{ maxWidth: 320 }} placeholder="Their external ID" value={ext} onChange={(e) => setExt(e.target.value)} aria-label="Contact external ID" data-no-dirty />
          <button className="btn" disabled={!ext.trim()}>Show picks</button>
        </form>
        <ErrorNote error={q.error} />
        {q.data && (
          <>
            <p className="small muted">Bought lately: {q.data.bought.slice(0, 5).join(", ") || "nothing"} · Looked at: {q.data.viewed.slice(0, 5).join(", ") || "nothing"}</p>
            {q.data.products.length === 0 ? <p className="small">No products to recommend yet: the catalog needs products in stock, and the lists need product events.</p> : (
              <div className="catalog-cards">{q.data.products.map((p, i) => (
                <div key={p.code} className="catalog-card">
                  {p.image_url ? <img src={p.image_url} alt="" /> : <div className="catalog-noimg" aria-hidden="true">🛍️</div>}
                  <strong>{i + 1}. {p.name}</strong><span className="small muted">{priceText(p)}</span>
                </div>
              ))}</div>
            )}
            {q.data.last_viewed && <p className="small">Last viewed without buying: <strong>{q.data.last_viewed.name}</strong> (<code>{"{{reco.last_viewed.name}}"}</code>)</p>}
          </>
        )}
      </div>
    </section>
  );
}

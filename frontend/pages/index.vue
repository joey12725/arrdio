<script setup lang="ts">
import type { Book, Candidate } from "~/types/acquisition";
type Suggestion = {
  id: string;
  title: string;
  author: string;
  cover?: string;
  kind?: string;
};
const title = ref(""),
  author = ref(""),
  kind = ref("both"),
  volume = ref("");
const selectedCatalog = ref(""),
  suggestions = ref<Suggestion[]>([]),
  busy = ref(false),
  error = ref("");
const books = ref<Book[]>([]),
  selected = ref<number[]>([]),
  sort = ref("queue"),
  page = ref(0),
  filter = ref("");
const selectedFormat = ref("");
const hard = reactive({
  language: "",
  narrator: "",
  edition: "",
  container: "",
});
const quota = ref<{
  remaining: number | null;
  reset: number | null;
  state: string;
}>({ remaining: null, reset: null, state: "checking" });
const counts = ref<Record<string, number>>({}),
  dispatch = ref(false),
  bulk = ref(""),
  showBulk = ref(false);
const services = ref<Record<string, string>>({});
const serviceIssues = computed(() =>
  Object.entries(services.value).filter(([, state]) =>
    /not configured|unavailable/.test(state),
  ),
);
const photos = ref<
  { id: string; name?: string; state: string; error?: string }[]
>([]);
const now = ref(Date.now());
const resetText = computed(() =>
  quota.value.reset
    ? `${Math.max(0, Math.ceil((quota.value.reset * 1000 - now.value) / 3600000))}h to next reset`
    : "Reset time unavailable",
);
const visible = computed(() => books.value);
const correction = ref<{
  id: number;
  format: string;
  candidates: Candidate[];
} | null>(null);
function toggleSelected(id: number) {
  selected.value = selected.value.includes(id)
    ? selected.value.filter((v) => v !== id)
    : [...selected.value, id];
}
async function changeResult(id: number, format: string) {
  try {
    const candidates = await api<Candidate[]>(
      `candidates?id=${id}&format=${format}`,
    );
    correction.value = { id, format, candidates };
  } catch {
    error.value = "Alternative results are temporarily unavailable.";
  }
}
async function correct(candidate: string) {
  if (!correction.value) return;
  try {
    await api("correct", {
      method: "POST",
      body: {
        id: correction.value.id,
        format: correction.value.format,
        candidate,
      },
    });
    correction.value = null;
    await refresh();
  } catch {
    error.value =
      "An active or unresolved transfer must finish reconciling before its result can be replaced.";
  }
}
let refreshSequence = 0;
let searchTimer: ReturnType<typeof setTimeout>,
  timer: ReturnType<typeof setInterval>;
let searchSequence = 0;
const api = <T,>(path: string, options?: any) =>
  $fetch<T>(`/api/arrdio/${path}`, options);
async function refresh() {
  const sequence = ++refreshSequence;
  try {
    const [items, q, s, p] = await Promise.all([
      api<Book[]>(
        `requests?limit=50&offset=${page.value * 50}&sort=${sort.value}&q=${encodeURIComponent(filter.value)}`,
      ),
      api<typeof quota.value>("quota"),
      api<{
        representations: Record<string, number>;
        dispatch_enabled: boolean;
        services: Record<string, string>;
      }>("status"),
      api<typeof photos.value>("photos"),
    ]);
    if (sequence !== refreshSequence) return;
    books.value = items;
    quota.value = q;
    counts.value = s.representations;
    dispatch.value = s.dispatch_enabled;
    photos.value = p;
    services.value = s.services || {};
  } catch (e: any) {
    error.value =
      e.statusMessage ||
      "Could not refresh the queue. Your saved requests are retained.";
  }
}
watch([title, author], () => {
  clearTimeout(searchTimer);
  const seq = ++searchSequence;
  searchTimer = setTimeout(async () => {
    if (title.value.trim().length < 2) {
      suggestions.value = [];
      return;
    }
    try {
      const data = await api<Suggestion[]>(
        `catalog?q=${encodeURIComponent(`${title.value} ${author.value}`)}`,
      );
      if (seq === searchSequence) suggestions.value = data;
    } catch {
      suggestions.value = [];
    }
  }, 350);
});
function choose(s: Suggestion) {
  title.value = s.title;
  author.value = s.author;
  selectedCatalog.value = s.id;
  if (s.kind === "manga" && kind.value === "both") kind.value = "manga";
  suggestions.value = [];
  ++searchSequence;
  clearTimeout(searchTimer);
}
function unpin() {
  selectedCatalog.value = "";
}
function inputBook(t: string, a = "") {
  return {
    title: t,
    author: a,
    catalog_id: selectedCatalog.value,
    kind: kind.value === "manga" ? "manga" : "book",
    volume: volume.value,
    formats: kind.value === "both" ? ["ebook", "audio"] : [kind.value],
    constraints: { ...hard },
  };
}
const pendingSubmission = ref<{
  key: string;
  items: any[];
  offset: number;
} | null>(null);
async function enqueue(items: any[]) {
  busy.value = true;
  error.value = "";
  if (!pendingSubmission.value)
    pendingSubmission.value = { key: crypto.randomUUID(), items, offset: 0 };
  try {
    while (
      pendingSubmission.value.offset < pendingSubmission.value.items.length
    ) {
      const batch = pendingSubmission.value;
      const result = await api<{ errors?: { index: number; error: string }[] }>(
        "requests",
        {
          method: "POST",
          headers: { "Idempotency-Key": `${batch.key}:${batch.offset}` },
          body: { items: batch.items.slice(batch.offset, batch.offset + 1000) },
        },
      );
      if (result.errors?.length)
        error.value = `${result.errors.length} entries in this chunk were invalid; the valid books were saved.`;
      batch.offset += 1000;
    }
    pendingSubmission.value = null;
    title.value = "";
    author.value = "";
    selectedCatalog.value = "";
    bulk.value = "";
    suggestions.value = [];
    kind.value = "both";
    volume.value = "";
    await refresh();
  } catch (e: any) {
    if ([400, 409, 413, 422].includes(e.statusCode))
      pendingSubmission.value = null;
    error.value =
      e.statusMessage ||
      "Submission interrupted. Retry sends the same batch safely.";
  } finally {
    busy.value = false;
  }
}
async function add() {
  if (title.value.trim() || pendingSubmission.value)
    await enqueue([inputBook(title.value.trim(), author.value.trim())]);
}
async function addBulk() {
  const items = bulk.value
    .split("\n")
    .map((s) => s.trim())
    .filter(Boolean)
    .map((line) => {
      const [t, a] = line.split(" | ");
      return { ...inputBook(t || "", a || ""), catalog_id: "" };
    });
  if (items.length) await enqueue(items);
}
async function control(id: number, action: string, format = "") {
  try {
    await api(`requests/${id}/${action}${format ? `?format=${format}` : ""}`, {
      method: "POST",
    });
    await refresh();
  } catch {
    error.value = "The operation could not be saved. Please retry.";
  }
}
async function cancelSelected() {
  for (const id of selected.value)
    await control(id, "cancel", selectedFormat.value);
  selected.value = [];
}
function base64(blob: Blob) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",")[1] || "");
    reader.onerror = reject;
    reader.readAsDataURL(blob);
  });
}
// Phone cameras exceed the service's 24 MP / 10 MB limits (a default iPhone
// photo is 24.5 MP). Larger photos are scaled to a 3072 px long edge, which
// keeps spine text legible; photos already within limits are sent unchanged.
async function photoPayload(file: File) {
  const maxEdge = 3072;
  // Desktop Chrome cannot decode HEIC (iPhone photos copied off the phone).
  const bitmap = await createImageBitmap(file).catch(() => {
    throw new Error("unreadable image");
  });
  const { width, height } = bitmap;
  const supported = ["image/png", "image/jpeg", "image/webp"].includes(
    file.type,
  );
  if (
    supported &&
    file.size <= 10 * 1024 * 1024 &&
    width * height <= 24_000_000 &&
    Math.max(width, height) <= 8192
  ) {
    bitmap.close();
    return { mime: file.type, data: await base64(file) };
  }
  const scale = Math.min(1, maxEdge / Math.max(width, height));
  const canvas = document.createElement("canvas");
  canvas.width = Math.round(width * scale);
  canvas.height = Math.round(height * scale);
  canvas.getContext("2d")!.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
  bitmap.close();
  const blob = await new Promise<Blob | null>((resolve) =>
    canvas.toBlob(resolve, "image/jpeg", 0.9),
  );
  if (!blob) throw new Error("photo could not be prepared");
  return { mime: "image/jpeg", data: await base64(blob) };
}
async function upload(event: Event) {
  const importKind = kind.value;
  const importRequirements = { ...hard };
  const input = event.target as HTMLInputElement;
  const files = Array.from(input.files || []);
  busy.value = true;
  for (const file of files) {
    try {
      const { mime, data } = await photoPayload(file);
      await api("photos", {
        method: "POST",
        body: {
          mime,
          data,
          name: file.name,
          constraints: importRequirements,
          ...(importKind === "both" ? {} : { formats: [importKind] }),
          ...(importKind === "manga" ? { kind: "manga" } : {}),
        },
      });
    } catch (e) {
      error.value =
        e instanceof Error && e.message === "unreadable image"
          ? `${file.name}: this browser cannot read that image type; use a JPEG or PNG, or upload from your phone`
          : `Could not upload ${file.name}; other photos were retained`;
    }
  }
  busy.value = false;
  input.value = "";
  await refresh();
}
watch([sort, page], refresh);
watch(filter, () => {
  page.value = 0;
  refresh();
});
onMounted(() => {
  refresh();
  timer = setInterval(() => {
    now.value = Date.now();
    refresh();
  }, 5000);
});
onBeforeUnmount(() => {
  clearInterval(timer);
  clearTimeout(searchTimer);
});
</script>

<template>
  <section class="hero">
    <div>
      <h1>Books</h1>
    </div>
    <div class="quota">
      <strong>{{ quota.remaining ?? "—" }}</strong
      ><span>Ebook quota remaining</span
      ><small v-if="quota.remaining !== null && quota.remaining < 5">{{
        resetText
      }}</small
      ><small v-else-if="quota.remaining === null">Checking quota</small>
    </div>
  </section>
  <div v-if="error" class="notice" role="alert">{{ error }}</div>
  <details v-if="serviceIssues.length" class="service-status">
    <summary>{{ serviceIssues.length }} integrations need attention</summary>
    <p v-for="[name, state] in serviceIssues" :key="name">
      {{ name }}: {{ state }}
    </p>
  </details>
  <section class="panel intake" aria-labelledby="add-title">
    <h2 id="add-title">Add books</h2>
    <form @submit.prevent="add" class="add-form">
      <label class="field title-field"
        >Title<input
          v-model="title"
          @input="unpin"
          placeholder="Book title"
          enterkeyhint="go"
          autocomplete="off"
          :required="!pendingSubmission"
      /></label>
      <label class="field"
        >Author <span class="optional">optional</span
        ><input v-model="author" @input="unpin" placeholder="Author name"
      /></label>
      <label class="field"
        >Formats<select v-model="kind">
          <option value="both">Both</option>
          <option value="ebook">Ebook</option>
          <option value="audio">Audiobook</option>
          <option value="manga">Manga</option>
        </select></label
      >
      <label v-if="kind === 'manga'" class="field"
        >Volume<input v-model="volume" placeholder="e.g. 1"
      /></label>
      <button class="button" :disabled="busy">
        {{ pendingSubmission ? "Retry saved batch" : "Add to queue" }}
      </button>
    </form>
    <details class="requirements">
      <summary>Specific requirements</summary>
      <div class="requirement-fields">
        <label class="field"
          >Language<input
            v-model="hard.language"
            placeholder="e.g. English" /></label
        ><label class="field"
          >Narrator (audio)<input v-model="hard.narrator" /></label
        ><label class="field">Edition<input v-model="hard.edition" /></label
        ><label class="field"
          >Required container<input
            v-model="hard.container"
            placeholder="e.g. epub or m4b"
        /></label>
      </div>
      <p>
        Only exact, verified requirements are accepted. Leave blank for the
        widest availability.
      </p>
    </details>
    <p v-if="selectedCatalog" class="pinned">✓ Catalog selection pinned</p>
    <ul
      v-if="suggestions.length && !selectedCatalog"
      class="suggestions"
      aria-label="Book suggestions"
    >
      <li v-for="s in suggestions" :key="s.id">
        <button @click="choose(s)">
          <img v-if="s.cover" :src="s.cover" alt="" /><span
            ><strong>{{ s.title }}</strong
            ><small>{{ s.author }}</small></span
          ><span aria-hidden="true">↗</span>
        </button>
      </li>
    </ul>
    <div class="intake-tools">
      <button class="text-button" @click="showBulk = !showBulk">
        {{ showBulk ? "Hide list import" : "+ Paste a book list" }}</button
      ><label class="photo-button"
        >▧ Add book photos<input
          type="file"
          accept="image/*"
          multiple
          @change="upload"
          :disabled="busy"
      /></label>
    </div>
    <form v-if="showBulk" @submit.prevent="addBulk" class="bulk">
      <label class="field"
        >One book per line · Title | Author<textarea
          v-model="bulk"
          rows="5"
          placeholder="The Jungle | Upton Sinclair"
        /></label
      ><button class="button" :disabled="busy || !bulk.trim()">
        Import list
      </button>
    </form>
    <p
      v-for="photo in photos.filter((p) => p.state !== 'complete' || p.error)"
      :key="photo.id"
      class="muted"
    >
      {{ photo.name || "Photo" }}: {{ photo.state }} {{ photo.error }}
    </p>
  </section>
  <section class="queue-section" aria-labelledby="queue-title">
    <div class="queue-heading">
      <div>
        <h2 id="queue-title">Queue</h2>
      </div>
      <span class="muted"
        >{{ Object.values(counts).reduce((a, b) => a + b, 0) }} format jobs
        <span v-if="!dispatch">· Workers paused</span></span
      >
    </div>
    <div class="queue-toolbar">
      <label class="field"
        >Sort<select v-model="sort">
          <option value="queue">Execution order</option>
          <option value="title">Title A–Z</option>
          <option value="-title">Title Z–A</option>
          <option value="author">Author A–Z</option>
          <option value="-author">Author Z–A</option>
        </select></label
      ><label class="field"
        >Filter queue<input
          v-model="filter"
          placeholder="Title or author" /></label
      ><template v-if="selected.length"
        ><label class="field"
          >Selected formats<select v-model="selectedFormat">
            <option value="">All requested formats</option>
            <option value="ebook">Ebook only</option>
            <option value="audio">Audio only</option>
            <option value="manga">Manga only</option>
          </select></label
        ><button class="button danger" @click="cancelSelected">
          Cancel {{ selected.length }} selected
        </button></template
      >
    </div>
    <div v-if="!visible.length" class="panel empty">
      <span class="empty-icon" aria-hidden="true">✦</span>
      <h2>No books in this view</h2>
    </div>
    <section v-if="correction" class="panel" aria-label="Change result">
      <div class="actions">
        <h2>Choose a different {{ correction.format }}</h2>
        <button class="button secondary" @click="correction = null">
          Close
        </button>
      </div>
      <p class="muted">
        Replacement updates only this request’s unchanged, app-owned library
        copies. Original downloads and unrelated files are kept.
      </p>
      <p v-if="!correction.candidates.length" class="muted">
        No alternative results are available yet.
      </p>
      <ul class="suggestions">
        <li v-for="candidate in correction.candidates" :key="candidate.id">
          <button @click="correct(candidate.id)">
            <span
              ><strong>{{ candidate.title }}</strong
              ><small
                >{{ candidate.author }} ·
                {{ candidate.extension || candidate.provider }}</small
              ></span
            >
            <span>Replace with this result</span>
          </button>
        </li>
      </ul>
    </section>
    <div class="book-grid">
      <AcquisitionCard
        v-for="book in visible"
        :key="book.id"
        :book="book"
        :selected="selected.includes(book.id)"
        @select="toggleSelected"
        @control="control"
        @correct="changeResult"
      />
    </div>
    <div class="pagination">
      <button class="button secondary" :disabled="page === 0" @click="page--">
        Previous</button
      ><span>Page {{ page + 1 }}</span
      ><button
        class="button secondary"
        :disabled="books.length < 50"
        @click="page++"
      >
        Next
      </button>
    </div>
  </section>
</template>

<style scoped>
.requirements {
  margin-top: 16px;
  font-size: 13px;
  color: var(--muted);
}
.requirements summary {
  cursor: pointer;
  min-height: 44px;
  display: flex;
  align-items: center;
}
.requirement-fields {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.requirements p {
  font-size: 12px;
  line-height: 1.5;
}
.service-status {
  margin: 0 0 20px;
  color: #ffb0da;
  font-size: 13px;
}
.service-status summary {
  cursor: pointer;
}
.service-status p {
  margin: 10px 0;
  color: var(--muted);
}
.intake {
  border-top: 2px solid var(--pink);
}
.hero {
  display: flex;
  justify-content: space-between;
  gap: 32px;
  align-items: center;
  margin-bottom: 36px;
}
.hero {
  margin-bottom: 20px;
  gap: 16px;
}
.hero h1 {
  margin: 0;
}
.quota {
  padding-left: 20px;
}
.quota strong {
  font-size: 32px;
}
.photo-button {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 44px;
  min-width: 150px;
  padding: 10px 14px;
  border: 1px solid #47556d;
  border-radius: 8px;
}
.photo-button input {
  position: absolute;
  inset: 0;
  opacity: 0;
  cursor: pointer;
  width: 100%;
  height: 100%;
  margin: 0;
}
.photo-button:focus-within {
  outline: 2px solid var(--mint);
  outline-offset: 3px;
}
.text-button {
  min-height: 44px;
  padding: 8px;
}
.queue-heading h2 {
  margin-bottom: 0;
}
.queue-toolbar {
  margin-top: 16px;
}
.book-grid {
  align-items: start;
}
.hero p {
  max-width: 650px;
  line-height: 1.7;
}
.quota {
  flex-shrink: 0;
  text-align: center;
  display: flex;
  flex-direction: column;
  border-left: 1px solid #38465a;
  padding-left: 36px;
}
.quota strong {
  font-size: 46px;
  color: var(--mint);
  font-family: "Tilt Neon", sans-serif;
}
.quota span {
  font-size: 12px;
}
.quota small {
  font-size: 11px;
  color: var(--muted);
  margin-top: 8px;
}
.add-form {
  display: flex;
  gap: 14px;
  align-items: end;
  flex-wrap: wrap;
}
.add-form .field {
  flex: 1;
  min-width: 140px;
  position: relative;
}
.add-form .title-field {
  flex: 2;
  min-width: 240px;
}
.optional {
  position: absolute;
  right: 0;
  color: #9aa9bf;
  font-size: 11px;
}
.intake-tools {
  display: flex;
  align-items: center;
  gap: 22px;
  margin-top: 22px;
  font-size: 12px;
  flex-wrap: wrap;
}
.text-button,
.photo-button {
  background: none;
  border: 0;
  color: var(--mint);
  padding: 0;
  cursor: pointer;
}
.photo-button input {
  display: block;
  width: 190px;
  font-size: 11px;
  margin-top: 6px;
}
.intake-tools > .muted {
  margin-left: auto;
}
.suggestions {
  list-style: none;
  padding: 6px;
  margin: 12px 0 0;
  background: #0d1526;
  border: 1px solid #41516a;
  border-radius: 12px;
  max-height: 350px;
  overflow: auto;
}
.suggestions button {
  display: flex;
  gap: 14px;
  align-items: center;
  width: 100%;
  text-align: left;
  background: none;
  border: 0;
  padding: 10px;
  color: white;
  border-radius: 8px;
}
.suggestions button:hover {
  background: #253248;
}
.suggestions img {
  width: 35px;
  height: 50px;
  object-fit: cover;
}
.suggestions small {
  display: block;
  color: var(--muted);
  margin-top: 4px;
}
.pinned {
  font-size: 12px;
  color: var(--mint);
}
.bulk {
  display: grid;
  gap: 12px;
  margin-top: 20px;
}
.queue-section {
  margin-top: 44px;
}
.queue-heading {
  display: flex;
  justify-content: space-between;
  gap: 18px;
  align-items: center;
}
.queue-heading h2 {
  font-size: 30px;
}
.queue-toolbar {
  display: flex;
  gap: 16px;
  align-items: end;
  margin-bottom: 22px;
}
.queue-toolbar .field {
  max-width: 260px;
  flex: 1;
}
.book-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 18px;
}
.book-card {
  display: flex;
  gap: 18px;
  background: #151e30;
  border: 1px solid #303e54;
  border-radius: 16px;
  padding: 20px;
}
.book-cover {
  width: 94px;
  min-width: 94px;
  height: 140px;
  background: linear-gradient(155deg, #314564, #122c35);
  border-radius: 4px 10px 10px 4px;
  border-left: 5px solid #567187;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  color: var(--mint);
}
.book-cover.manga {
  background: linear-gradient(140deg, #562650, #222944);
  color: #ffa5df;
}
.book-cover span {
  font-size: 42px;
}
.book-cover small {
  font-size: 9px;
  letter-spacing: 2px;
  margin-top: 15px;
}
.book-main {
  flex: 1;
  min-width: 0;
}
.select-book {
  float: right;
  display: flex;
  align-items: center;
  gap: 6px;
  color: #97a6bc;
  font-size: 11px;
}
.book-card h3 {
  margin: 0 0 6px;
  font-size: 19px;
  line-height: 1.4;
}
.book-author {
  font-size: 13px;
  color: var(--muted);
  margin: 0 0 20px;
}
.representation {
  padding: 10px 0;
  border-top: 1px solid #303e54;
}
.representation > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}
.representation strong {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.7px;
}
.status {
  font-size: 11px;
  color: #becbe0;
  text-align: right;
}
.status.available {
  color: var(--mint);
}
.status.waiting_allowance,
.status.retryable_failure {
  color: #ffb0da;
}
.child-actions {
  justify-content: end !important;
}
.child-actions button,
.card-actions button {
  border: 0;
  background: none;
  color: #bacade;
  padding: 6px 0;
  font-size: 11px;
}
.child-actions button {
  margin-left: 10px;
}
.card-actions {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  border-top: 1px solid #303e54;
  margin-top: 10px;
  padding-top: 10px;
}
.card-actions button:first-child {
  color: var(--mint);
}
.empty-icon {
  font-size: 44px;
  color: var(--mint);
  display: block;
  margin-bottom: 20px;
}
.empty p {
  font-size: 14px;
  line-height: 1.8;
}
.pagination {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 20px;
  margin-top: 24px;
  font-size: 13px;
}
@media (max-width: 900px) {
  .book-grid {
    grid-template-columns: 1fr;
  }
  .hero {
    align-items: flex-start;
  }
  .quota {
    padding-left: 20px;
  }
  .intake-tools > .muted {
    margin-left: 0;
  }
}
@media (max-width: 600px) {
  .hero {
    flex-direction: column;
    gap: 12px;
  }
  .quota {
    border: 0;
    padding: 0;
    flex-direction: row;
    gap: 12px;
    align-items: baseline;
  }
  .quota strong {
    font-size: 32px;
  }
  .add-form {
    display: grid;
    grid-template-columns: 1fr;
  }
  .add-form .title-field {
    min-width: 0;
  }
  .queue-heading {
    align-items: flex-start;
    flex-direction: column;
  }
  .queue-heading h2 {
    margin-bottom: 0;
  }
  .queue-toolbar {
    margin-top: 20px;
    flex-wrap: wrap;
  }
  .book-card {
    padding: 14px;
    gap: 12px;
  }
  .book-cover {
    width: 62px;
    min-width: 62px;
    height: 100px;
  }
  .book-card h3 {
    font-size: 16px;
  }
}
.hero {
  margin-bottom: 20px;
  gap: 16px;
}
.hero h1 {
  margin: 0;
}
.quota {
  padding-left: 20px;
}
.quota strong {
  font-size: 32px;
}
.photo-button {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-height: 44px;
  min-width: 150px;
  padding: 10px 14px;
  border: 1px solid #47556d;
  border-radius: 8px;
}
.photo-button input {
  position: absolute;
  inset: 0;
  opacity: 0;
  cursor: pointer;
  width: 100%;
  height: 100%;
  margin: 0;
}
.photo-button:focus-within {
  outline: 2px solid var(--mint);
  outline-offset: 3px;
}
.text-button {
  min-height: 44px;
  padding: 8px;
}
.queue-heading h2 {
  margin-bottom: 0;
}
.queue-toolbar {
  margin-top: 16px;
}
.book-grid {
  align-items: start;
}
@media (max-width: 600px) {
  .hero {
    flex-direction: row;
    align-items: center;
  }
  .hero h1 {
    font-size: 30px;
  }
  .quota {
    padding: 0;
    border: 0;
    display: grid;
    grid-template-columns: auto auto;
    gap: 4px 8px;
    max-width: 190px;
    text-align: right;
  }
  .quota strong {
    font-size: 26px;
    grid-row: 1/3;
  }
  .quota span {
    font-size: 11px;
    align-self: center;
  }
  .quota small {
    font-size: 10px;
    grid-column: 2;
    margin: 0;
  }
  .add-form {
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    gap: 12px;
  }
  .add-form .field {
    min-width: 0;
  }
  .add-form .title-field {
    grid-column: 1/-1;
  }
  .add-form > .button {
    grid-column: 1/-1;
  }
  .intake-tools {
    gap: 8px;
    justify-content: space-between;
    margin-top: 12px;
  }
  .requirements {
    margin-top: 4px;
  }
  .queue-section {
    margin-top: 26px;
  }
  .queue-heading {
    flex-direction: row;
    align-items: center;
  }
  .queue-heading h2 {
    font-size: 24px;
  }
  .queue-heading > .muted {
    font-size: 11px;
    text-align: right;
    max-width: 145px;
  }
  .queue-toolbar {
    gap: 10px;
    margin-top: 14px;
  }
  .queue-toolbar .field {
    min-width: 0;
    max-width: none;
  }
  .requirement-fields {
    grid-template-columns: 1fr;
  }
  .suggestions button {
    min-height: 64px;
  }
  .pagination {
    gap: 12px;
  }
  .service-status summary {
    min-height: 44px;
    line-height: 1.5;
  }
  .empty {
    padding: 28px 16px;
  }
}
</style>

<script setup lang="ts">
import type { Book } from "~/types/acquisition";
defineProps<{ book: Book; selected: boolean }>();
const emit = defineEmits<{
  select: [id: number];
  control: [id: number, action: string, format?: string];
  correct: [id: number, format: string];
}>();
const brokenCover = ref(false);
// One line per library: where the item is available, only those links;
// otherwise one state per service rather than one per category route.
function shownPresence(
  list: { service: string; state: string; url?: string }[],
) {
  const available = list.filter((p) => p.state === "available");
  if (available.length) return available;
  const seen = new Set<string>();
  return list.filter((p) => {
    const key = `${p.service}:${p.state}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}
function safeURL(value?: string) {
  return value && /^https?:\/\//i.test(value) ? value : undefined;
}
</script>
<template>
  <article class="acquisition-card">
    <div class="cover">
      <img
        v-if="safeURL(book.cover) && !brokenCover"
        :src="book.cover"
        :alt="`Cover of ${book.title}`"
        loading="lazy"
        @error="brokenCover = true"
      />
      <div v-else class="placeholder">
        <span aria-hidden="true">▤</span
        ><small>{{ book.kind.toUpperCase() }}</small>
      </div>
    </div>
    <div class="content">
      <label class="select"
        ><input
          type="checkbox"
          :checked="selected"
          @change="emit('select', book.id)"
          :aria-label="`Select ${book.title}`"
        /><span>#{{ book.id }}</span></label
      >
      <h3>{{ book.title }}</h3>
      <p class="author">
        {{ book.author || "Author pending"
        }}<span v-if="book.volume"> · Volume {{ book.volume }}</span>
      </p>
      <p v-if="book.description" class="description">{{ book.description }}</p>
      <section
        v-for="child in book.representations"
        :key="child.id"
        class="representation"
        :aria-label="`${child.format} progress`"
      >
        <div class="status-row">
          <strong>{{ child.format }}</strong
          ><span :class="['status', child.state]">{{
            child.state.replaceAll("_", " ")
          }}</span>
        </div>
        <p
          v-if="
            child.selected &&
            (child.selected.title !== book.title ||
              (child.selected.author && child.selected.author !== book.author))
          "
          class="chosen-metadata"
        >
          <strong>{{ child.selected.title }}</strong
          ><span v-if="child.selected.author">
            · {{ child.selected.author }}</span
          >
        </p>
        <p v-if="child.error" class="error">{{ child.error }}</p>
        <p v-if="child.conversion_error" class="error">
          {{ child.conversion_error }}
        </p>
        <ul v-if="child.presence?.length" class="presence">
          <li
            v-for="(presence, index) in shownPresence(child.presence)"
            :key="`${presence.service}-${index}`"
          >
            <span>{{ presence.service }}: </span
            ><a
              v-if="presence.state === 'available' && safeURL(presence.url)"
              :href="presence.url"
              target="_blank"
              rel="noopener noreferrer"
              >Open in library ↗</a
            ><span v-else>{{
              presence.state === "unknown"
                ? "Availability unknown"
                : presence.state
            }}</span>
          </li>
        </ul>
        <div class="child-actions">
          <button
            v-if="!['available', 'canceled'].includes(child.state)"
            @click="emit('control', book.id, 'bump', child.format)"
            :aria-label="`Bump ${child.format} for ${book.title}`"
          >
            ↑ Front
          </button>
          <button @click="emit('correct', book.id, child.format)">
            Change result</button
          ><button
            v-if="['missing', 'retryable_failure'].includes(child.state)"
            @click="emit('control', book.id, 'retry', child.format)"
          >
            Retry</button
          ><button
            v-if="!['available', 'canceled'].includes(child.state)"
            @click="emit('control', book.id, 'cancel', child.format)"
            :aria-label="`Cancel ${child.format} for ${book.title}`"
          >
            Cancel
          </button>
        </div>
      </section>
      <div class="actions">
        <button @click="emit('control', book.id, 'bump')">
          ↑ Bump to front</button
        ><button @click="emit('control', book.id, 'cancel')">
          Cancel request
        </button>
      </div>
    </div>
  </article>
</template>
<style scoped>
.chosen-metadata {
  font-size: 13px;
  line-height: 1.5;
  color: #d6dfed;
  margin: 10px 0;
}
.chosen-metadata strong {
  font-weight: 600;
}
.acquisition-card {
  display: flex;
  gap: 20px;
  padding: 22px;
  border-radius: 16px;
  border: 1px solid #303e54;
  background: #151e30;
}
.cover {
  width: 104px;
  flex-shrink: 0;
}
.cover img,
.placeholder {
  width: 104px;
  min-height: 150px;
  max-height: 170px;
  object-fit: cover;
  border-radius: 5px 10px 10px 5px;
}
.placeholder {
  background: linear-gradient(155deg, #314564, #122c35);
  border-left: 5px solid #567187;
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  color: var(--mint);
}
.placeholder span {
  font-size: 42px;
}
.placeholder small {
  font-size: 9px;
  letter-spacing: 2px;
  margin-top: 15px;
}
.content {
  flex: 1;
  min-width: 0;
}
.select {
  float: right;
  display: flex;
  align-items: center;
  gap: 6px;
  color: #b3c3d9;
  font-size: 11px;
}
h3 {
  font-size: 19px;
  line-height: 1.4;
  margin: 0 0 6px;
}
.author {
  font-size: 13px;
  color: var(--muted);
  margin: 0 0 16px;
}
.description {
  font-size: 13px;
  line-height: 1.6;
  color: #d6dfed;
  display: -webkit-box;
  -webkit-line-clamp: 4;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.representation {
  padding: 12px 0;
  border-top: 1px solid #303e54;
}
.status-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}
.status-row strong {
  font-size: 11px;
  text-transform: uppercase;
  letter-spacing: 0.7px;
}
.status {
  font-size: 12px;
  color: #d2dcec;
}
.status.available {
  color: var(--mint);
}
.error {
  color: #ffb0da;
  font-size: 12px;
  line-height: 1.5;
}
.presence {
  list-style: none;
  padding: 0;
  margin: 8px 0;
  font-size: 11px;
  color: var(--muted);
}
.presence li {
  margin: 5px 0;
}
.child-actions,
.actions {
  display: flex;
  gap: 16px;
  flex-wrap: wrap;
}
button {
  background: none;
  border: 0;
  padding: 6px 0;
  font-size: 12px;
  color: #c2d1e6;
}
.actions {
  border-top: 1px solid #303e54;
  padding-top: 10px;
  justify-content: space-between;
}
.actions button:first-child {
  color: var(--mint);
}
@media (max-width: 600px) {
  .acquisition-card {
    padding: 15px;
    gap: 12px;
  }
  .cover,
  .cover img,
  .placeholder {
    width: 64px;
  }
  .cover img,
  .placeholder {
    min-height: 95px;
    max-height: 110px;
  }
  h3 {
    font-size: 17px;
  }
  .status-row {
    align-items: flex-start;
  }
  .status {
    text-align: right;
  }
}
.child-actions button,
.actions button {
  min-height: 44px;
  padding: 10px 4px;
}
.select {
  min-height: 44px;
  min-width: 44px;
  justify-content: center;
}
.select input {
  width: 20px;
  height: 20px;
}
.select span {
  display: none;
}
.content h3 {
  overflow-wrap: anywhere;
}
.child-actions {
  gap: 8px;
}
.actions {
  gap: 8px;
}
@media (max-width: 600px) {
  .acquisition-card {
    gap: 12px;
  }
  .description {
    -webkit-line-clamp: 3;
  }
  .child-actions {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .child-actions button {
    text-align: left;
    font-size: 12px;
  }
  .actions button {
    font-size: 12px;
  }
  .presence {
    font-size: 12px;
    line-height: 1.5;
  }
  .select {
    float: right;
    margin: -8px -4px 0 4px;
  }
  .cover {
    width: 64px;
  }
  .status-row {
    flex-wrap: wrap;
  }
}
</style>

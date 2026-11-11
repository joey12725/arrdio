export type Presence = {
  service: string;
  state: string;
  url?: string;
  item_id?: string;
};
export type Representation = {
  id: number;
  format: string;
  state: string;
  error?: string;
  presence?: Presence[];
  conversion?: string;
  conversion_error?: string;
  selected?: Candidate;
  retry_at?: number;
};
export type Book = {
  id: number;
  title: string;
  author: string;
  catalog_id?: string;
  volume?: string;
  kind: string;
  cover?: string;
  description?: string;
  representations: Representation[];
};
export type Candidate = {
  id: string;
  title: string;
  author: string;
  cover?: string;
  provider?: string;
  extension?: string;
};

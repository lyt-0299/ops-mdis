import { createAsyncThunk, createSelector,createSlice } from '@reduxjs/toolkit'

import { listSources } from '../../utils/alertApi'

/**
 * Fetch the aggregated data sources list (mixed kinds) with enabled state.
 * Backend returns a bare array; the axios instance resolves the full response,
 * so the payload is read from response.data.
 */
export const fetchSources = createAsyncThunk('sources/fetch', async () => {
  const res = await listSources();
  return res.data || [];
});

const sourcesSlice = createSlice({
  name: 'sources',
  initialState: {
    list: [],
    loading: false,
    loaded: false,
  },
  reducers: {},
  extraReducers: (builder) => {
    builder
      .addCase(fetchSources.pending, (state) => {
        state.loading = true;
      })
      .addCase(fetchSources.fulfilled, (state, action) => {
        state.list = action.payload;
        state.loading = false;
        state.loaded = true;
      })
      .addCase(fetchSources.rejected, (state) => {
        state.loading = false;
      });
  },
});

// ─── selectors ──────────────────────────────────────────────────────────────

/** Raw aggregated sources list. */
export const selectSources = (state) => state.sources.list;

/**
 * The set of distinct `type` values among enabled sources.
 * Memoized via createSelector so it returns a STABLE array reference when the
 * sources list is unchanged — otherwise useSelector's reference equality would
 * see a fresh array every render and loop forever (React error #185).
 * Used by the menu / alert pages to decide whether a given type is enabled.
 */
export const selectEnabledTypes = createSelector([selectSources], (list) => {
  const types = new Set();
  for (const item of list) {
    if (item && item.enabled === true && item.type) {
      types.add(item.type);
    }
  }
  return Array.from(types);
});

export default sourcesSlice.reducer;

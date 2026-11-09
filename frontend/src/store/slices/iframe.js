import { createSlice } from '@reduxjs/toolkit'

const iframeSlice = createSlice({
  name: 'iframe',
  initialState: {
    iframeUrls: {},
    menuItems: [],
    loading: false,
  },
  reducers: {
    updateIframeUrls: (state, action) => {
      state.iframeUrls = { ...state.iframeUrls, ...action.payload };
    },
    updateMenuItems: (state, action) => {
      state.menuItems = action.payload;
    },
    updateMenuLoading: (state, action) => {
      state.loading = action.payload;
    },
  },
});

export const {
  updateIframeUrls,
  updateMenuItems,
  updateMenuLoading,
} = iframeSlice.actions
export default iframeSlice.reducer;

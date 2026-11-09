import { configureStore } from '@reduxjs/toolkit'

import iframeReducer from './slices/iframe'
import sourcesReducer from './slices/sources'

export default configureStore({
  reducer: {
    iframe: iframeReducer,
    sources: sourcesReducer,
  },
})

 
const base = __webpack_public_path__ || '/web/';

const config = [
  {
    key: 'en',
    label: 'English',
    languageTeam: 'English',
    language: 'en_US',
    path: `${base}locale/en/mdTranslation.js`,
  },
  {
    key: 'zh-Hans',
    label: '简体中文',
    languageTeam: 'Chinese Simplified',
    language: 'zh_CN',
    path: `${base}locale/zh_Hans/mdTranslation.js`,
  },
  {
    key: 'zh-Hant',
    label: '繁體中文',
    languageTeam: 'Chinese Traditional',
    language: 'zh_TW',
    path: `${base}locale/zh_Hant/mdTranslation.js`,
  },
  {
    key: 'ja',
    label: '日本語',
    languageTeam: 'Japanese',
    language: 'ja',
    path: `${base}locale/ja/mdTranslation.js`,
  },
];

export default config;

const gulp = require('gulp');
const each = require('gulp-each');
const fetch = require('node-fetch');
const _ = require('lodash');

// 提取key
let langKeys = [];

// 静态页面
const getLangKey = function (done) {
  langKeys = [];

  return gulp
    .src('src/**/*.{js,jsx}')
    .pipe(
      each(function (content, file, callback) {
        content = content.replace(/_l\([\n|\r\n]\s*/g, '_l(').replace(/,[\n|\r\n]\s*\)/g, ')');

        let regStr = /(\[\[\[(.+?)\]\]\])|(_l\(('(.+?)'|"(.+?)"|`(.+?)`)(['"]?(\s*)?,(\s*)['"]?(.+?))*\))/;
        var reg = new RegExp(regStr, 'g');
        var reg1 = new RegExp(regStr);
        var matchs = content.match(reg);

        if (matchs) {
          matchs.forEach(function (item) {
            var groups = reg1.exec(item);
            if (groups) {
              key = groups[2] || groups[5] || groups[6] || groups[7];
              if (key != 'undefined' && langKeys.indexOf(key) === -1) {
                langKeys.push(key);
              }
            }
          });
        }
        callback(null, content);
      }),
    )
    .on('end', () => {
      if (langKeys.length) {
        console.log('keys total: ' + langKeys.reduce((a = [], b = []) => a.concat(b)).length);
      } else {
        console.log('keys is empty');
      }
      done();
    });
};

// push key
const pushLangKey = function (done) {
  const rows = _.uniq(langKeys).map(key => {
    return [
      {
        controlId: 'key',
        value: key,
      },
      {
        controlId: 'zh_Hans',
        value: key.replace(/%\d{5}$/, ''),
      },
    ];
  });

  console.log(`key ${rows.length} 个`);

  const sendRequests = async () => {
    for (const items of _.chunk(rows, 1000)) {
      await new Promise(resolve => setTimeout(resolve, 1000)); // 延迟 1 秒
      await fetch('https://api2.mingdao.com/v2/open/worksheet/addRows', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          appKey: 'a5d939a67aec0c6d',
          sign: 'MzlhMjllOWM1Yjg3ZjIyNTdmMGRhMTdjNTczZjYwZGM2NWEwNTg5MGNiZWUxNDc4MjhlYzAyZjIxM2ExMjk4Zg==',
          worksheetId: '67723589c72af1fa35c19fc5',
          triggerWorkflow: true,
          rows: items,
          allowPartialSuccess: true,
        }),
      });
    }

    done();
  };

  sendRequests();
};

// 提取key
gulp.task('getLangKey', getLangKey);

// push增量key
gulp.task('pushLangKey', gulp.series(['getLangKey'], pushLangKey));
// A Nuvio provider written like the real ones: it scrapes a page of the
// (fake) site with cheerio, calls its JSON API with axios and returns the
// streams. Promise chains, as the Nuvio docs recommend.
const cheerio = require('cheerio-without-node-native');
const CryptoJS = require('crypto-js');
const axios = require('axios');

const BASE = 'http://nuvio.test';

function pagePath(tmdbId, mediaType, season, episode) {
  if (mediaType === 'tv') {
    return '/tv/' + tmdbId + '/' + season + '/' + episode;
  }
  return '/movie/' + tmdbId;
}

function getStreams(tmdbId, mediaType, season, episode) {
  console.log('[Fixture] ' + mediaType + ' ' + tmdbId + (mediaType === 'tv' ? ' S' + season + 'E' + episode : ''));
  return fetch(BASE + pagePath(tmdbId, mediaType, season, episode), { headers: { 'User-Agent': 'Fixture/1.0' } })
    .then(function (res) {
      if (!res.ok) throw new Error('page answered ' + res.status);
      return res.text();
    })
    .then(function (html) {
      const $ = cheerio.load(html);
      const streams = [];
      $('.source').each(function (i, el) {
        const link = $(this).find('a');
        streams.push({
          name: 'Fixture',
          title: $(el).find('.label').text().trim(),
          url: link.attr('href'),
          quality: $(el).data('quality'),
          size: el.attribs['data-size'],
          headers: { Referer: BASE + '/', 'X-Index': i }
        });
      });
      const token = CryptoJS.MD5('kumobox').toString();
      return axios.get(BASE + '/api/sources', { params: { id: tmdbId, type: mediaType, token: token } })
        .then(function (r) {
          r.data.sources.forEach(function (s) {
            streams.push({ name: 'Fixture API', title: s.label, url: s.file, quality: s.res, type: s.type, subtitles: s.tracks });
          });
          return streams;
        });
    })
    .catch(function (err) {
      console.error('[Fixture] ' + err.message);
      return [];
    });
}

module.exports = { getStreams };

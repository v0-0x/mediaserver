// A provider in ES module syntax (KumoBox converts it to CommonJS).
import cheerio from 'cheerio-without-node-native';

export async function getStreams(tmdbId, mediaType) {
  const $ = cheerio.load('<a href="http://cdn.nuvio.test/esm/' + tmdbId + '.m3u8">720p</a>');
  const a = $('a');
  return [{ name: 'ESM', title: 'ESM ' + mediaType, url: a.attr('href'), quality: a.text() }];
}

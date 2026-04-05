import type * as Preset from '@docusaurus/preset-classic';
import type { Config } from '@docusaurus/types';
import { themes as prismThemes } from 'prism-react-renderer';

const config: Config = {
    title: 'Vylux',
    tagline:
        'An independent media processing service for real-time images, video previews, and encrypted HLS CMAF streaming.',
    favicon: 'img/favicon.ico',

    // Set the production url of your site here
    url: 'https://carry0987.github.io',
    // Set the /<baseUrl>/ pathname under which your site is served
    // For GitHub pages deployment, it is often '/<projectName>/'
    baseUrl: '/Vylux/',

    // GitHub pages deployment config.
    // If you aren't using GitHub pages, you don't need these.
    organizationName: 'carry0987',
    projectName: 'Vylux',

    headTags: [
        {
            tagName: 'meta',
            attributes: {
                name: 'algolia-site-verification',
                content: 'C256838A766C253A',
            },
        },
    ],

    // The broken links detection is only available for a production build
    onBrokenLinks: 'throw',

    // Global markdown configuration
    markdown: {
        mermaid: true,
        hooks: {
            onBrokenMarkdownLinks: 'warn',
            onBrokenMarkdownImages: 'throw',
        },
    },

    // Even if you don't use internationalization, you can use this field to set
    // useful metadata like html lang. For example, if your site is Chinese, you
    // may want to replace "en" with "zh-Hant".
    i18n: {
        defaultLocale: 'en',
        locales: ['en', 'zh-TW'],
        localeConfigs: {
            en: {
                label: 'English',
                htmlLang: 'en-US',
            },
            'zh-TW': {
                label: '繁體中文',
                htmlLang: 'zh-TW',
            },
        },
    },

    presets: [
        [
            '@docusaurus/preset-classic',
            {
                blog: false,
                sitemap: {
                    changefreq: 'weekly',
                    priority: 0.5,
                },
                docs: {
                    sidebarPath: './sidebars.ts',
                    showLastUpdateAuthor: true,
                    showLastUpdateTime: true,
                    editUrl: 'https://github.com/carry0987/Vylux/tree/gh-pages/',
                },
                theme: {
                    customCss: './src/css/global.custom.css',
                },
            } satisfies Preset.Options,
        ],
    ],

    themeConfig: {
        navbar: {
            hideOnScroll: false,
            title: 'Vylux',
            items: [
                {
                    to: 'docs/intro',
                    activeBasePath: 'docs',
                    position: 'left',
                    label: 'Docs',
                },
                {
                    to: 'docs/architecture/overview',
                    position: 'left',
                    label: 'Architecture',
                },
                {
                    href: 'https://github.com/carry0987/Vylux',
                    label: 'GitHub',
                    position: 'right',
                },
                {
                    type: 'localeDropdown',
                    position: 'right',
                },
            ],
        },
        footer: {
            style: 'dark',
            copyright: `Copyright © ${new Date().getFullYear()} carry0987. Vylux docs built with Docusaurus.`,
        },
        colorMode: {
            defaultMode: 'light',
            disableSwitch: false,
            respectPrefersColorScheme: true,
        },
        prism: {
            theme: prismThemes.oneDark,
            darkTheme: prismThemes.oneDark,
            additionalLanguages: ['tsx', 'css', 'json', 'bash', 'go', 'sql', 'docker'],
        },
        liveCodeBlock: {
            /**
             * The position of the live playground, above or under the editor
             * Possible values: "top" | "bottom"
             */
            playgroundPosition: 'bottom',
        },
        algolia: {
            appId: 'KARSYN8W9L',
            apiKey: 'a881f0ccad1d269cf7971fe8836e994f',
            indexName: 'Vylux Index',
            contextualSearch: true,
            externalUrlRegex: 'external\\.com|domain\\.com',
            searchParameters: {},
            searchPagePath: 'search',
            insights: false,
        },
    } satisfies Preset.ThemeConfig,
    themes: ['@docusaurus/theme-mermaid'],
};

export default config;

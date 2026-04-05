import type { SidebarsConfig } from '@docusaurus/plugin-content-docs';

/**
 * Creating a sidebar enables you to:
    - create an ordered group of docs
    - render a sidebar for each doc of that group
    - provide next/previous navigation

    The sidebars can be generated from the filesystem, or explicitly defined here.

    Create as many sidebars as you want.
 */
const sidebars: SidebarsConfig = {
    docs: [
        'intro',
        'getting-started',
        'integration-guide',
        'integration-recipes',
        {
            type: 'category',
            label: 'Architecture',
            items: ['architecture/overview', 'architecture/request-lifecycle', 'architecture/storage-layout'],
        },
        {
            type: 'category',
            label: 'Media Pipelines',
            items: ['media/image-pipeline', 'media/video-pipeline', 'media/encrypted-streaming'],
        },
        {
            type: 'category',
            label: 'HTTP APIs',
            items: ['api/jobs', 'api/image-delivery', 'api/playback', 'api/cleanup', 'api/system'],
        },
        {
            type: 'category',
            label: 'Operations',
            items: ['operations/configuration', 'operations/deployment', 'operations/observability'],
        },
        {
            type: 'category',
            label: 'Development',
            items: ['development/testing', 'development/extending'],
        },
    ],
};

export default sidebars;

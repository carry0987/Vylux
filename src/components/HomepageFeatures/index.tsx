import Translate from '@docusaurus/Translate';
import Heading from '@theme/Heading';
import clsx from 'clsx';
import type { ReactNode } from 'react';
import styles from './styles.module.css';

type FeatureItem = {
    eyebrowId: string;
    eyebrow: string;
    titleId: string;
    title: string;
    descriptionId: string;
    description: string;
};

const FeatureList: FeatureItem[] = [
    {
        eyebrowId: 'homepage.features.runtime.eyebrow',
        eyebrow: 'Runtime',
        titleId: 'homepage.features.runtime.title',
        title: 'One service, two workloads',
        descriptionId: 'homepage.features.runtime.description',
        description:
            'Vylux combines synchronous image delivery with asynchronous media jobs while keeping storage, queueing, and routing conventions consistent.',
    },
    {
        eyebrowId: 'homepage.features.streaming.eyebrow',
        eyebrow: 'Streaming',
        titleId: 'homepage.features.streaming.title',
        title: 'Modern video pipeline',
        descriptionId: 'homepage.features.streaming.description',
        description:
            'FFmpeg handles encoding, Shaka Packager builds HLS CMAF, and the service supports AV1 and H.264 ladders with encrypted key delivery.',
    },
    {
        eyebrowId: 'homepage.features.operations.eyebrow',
        eyebrow: 'Operations',
        titleId: 'homepage.features.operations.title',
        title: 'Built for real deployments',
        descriptionId: 'homepage.features.operations.description',
        description:
            'PostgreSQL state, Redis queues, Prometheus metrics, OpenTelemetry traces, cleanup workflows, and integration coverage are part of the core shape.',
    },
];

function Feature({ eyebrowId, eyebrow, titleId, title, descriptionId, description }: FeatureItem) {
    return (
        <div className={clsx('col col--4', styles.featureCol)}>
            <div className={styles.featureCard}>
                <p className={styles.featureEyebrow}>
                    <Translate id={eyebrowId}>{eyebrow}</Translate>
                </p>
                <Heading as="h3">
                    <Translate id={titleId}>{title}</Translate>
                </Heading>
                <p>
                    <Translate id={descriptionId}>{description}</Translate>
                </p>
            </div>
        </div>
    );
}

export default function HomepageFeatures(): ReactNode {
    return (
        <section className={styles.features}>
            <div className="container">
                <div className={styles.sectionHeader}>
                    <Heading as="h2">
                        <Translate id="homepage.features.section.title">What This Docs Site Covers</Translate>
                    </Heading>
                    <p>
                        <Translate id="homepage.features.section.description">
                            Architecture, HTTP APIs, media pipelines, deployment concerns, and the operational details
                            required to run Vylux with confidence.
                        </Translate>
                    </p>
                </div>
                <div className="row">
                    {FeatureList.map((props) => (
                        <Feature key={props.title} {...props} />
                    ))}
                </div>
            </div>
        </section>
    );
}

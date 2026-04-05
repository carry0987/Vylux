import Link from '@docusaurus/Link';
import Translate, { translate } from '@docusaurus/Translate';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import HomepageFeatures from '@site/src/components/HomepageFeatures';
import Heading from '@theme/Heading';
import Layout from '@theme/Layout';
import clsx from 'clsx';
import type { ReactNode } from 'react';

import styles from './index.module.css';

function HomepageHeader() {
    const { siteConfig } = useDocusaurusContext();

    return (
        <header className={clsx(styles.heroBanner)}>
            <div className={clsx('container', styles.heroInner)}>
                <div className={styles.heroCopy}>
                    <p className={styles.kicker}>
                        <Translate id="homepage.hero.kicker">Media Processing Platform</Translate>
                    </p>
                    <Heading as="h1" className={styles.heroTitle}>
                        {siteConfig.title}
                    </Heading>
                    <p className={styles.heroSubtitle}>
                        <Translate id="homepage.hero.subtitle">
                            An independent media processing service for real-time images, video previews, and encrypted
                            HLS CMAF streaming.
                        </Translate>
                    </p>
                    <div className={styles.buttons}>
                        <Link className="button button--primary button--lg" to="/docs/intro">
                            <Translate id="homepage.hero.primaryButton">Read the Docs</Translate>
                        </Link>
                        <Link className="button button--outline button--lg" to="/docs/architecture/overview">
                            <Translate id="homepage.hero.secondaryButton">View Architecture</Translate>
                        </Link>
                    </div>
                </div>
                <div className={styles.heroPanel}>
                    <div className={styles.panelLabel}>
                        <Translate id="homepage.hero.panelLabel">Validated Scope</Translate>
                    </div>
                    <ul className={styles.panelList}>
                        <li>
                            <Translate id="homepage.hero.scope.image">
                                Real-time image resize, format conversion, and signed URLs
                            </Translate>
                        </li>
                        <li>
                            <Translate id="homepage.hero.scope.video">
                                Video cover generation, animated previews, and HLS CMAF transcoding
                            </Translate>
                        </li>
                        <li>
                            <Translate id="homepage.hero.scope.encryption">
                                Raw-key CBCS encryption with Bearer-token key delivery
                            </Translate>
                        </li>
                        <li>
                            <Translate id="homepage.hero.scope.ops">
                                Redis queues, PostgreSQL job state, tracing, metrics, and cleanup
                            </Translate>
                        </li>
                    </ul>
                </div>
            </div>
        </header>
    );
}

export default function Home(): ReactNode {
    const { siteConfig } = useDocusaurusContext();

    return (
        <Layout
            title={siteConfig.title}
            description={translate({
                id: 'homepage.layout.description',
                message:
                    'Vylux documentation covering system architecture, HTTP APIs, media pipelines, and operational details.',
            })}>
            <HomepageHeader />
            <main>
                <HomepageFeatures />
            </main>
        </Layout>
    );
}
